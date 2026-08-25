package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vertex/bff/internal/client"
	"github.com/vertex/bff/internal/config"
	"github.com/vertex/bff/internal/graph"
)

// fakeUpstream แทน service จริงทั้งสามตัว
//
// ใช้ของปลอมเพราะสิ่งที่ต้องพิสูจน์คือพฤติกรรมของ BFF เอง —
// ว่าส่ง token ต่อไหม ยิงซ้ำกี่ครั้ง และจัดการ error อย่างไร
type fakeUpstream struct {
	srv *httptest.Server

	authCalls  int32
	petCalls   int32
	eventFails bool

	// lastAuthHeader เก็บไว้ตรวจว่า JWT ของผู้เรียกถูกส่งต่อจริง
	lastAuthHeader atomic.Value
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		f.lastAuthHeader.Store(r.Header.Get("Authorization"))
		writeJSON(w, map[string]any{
			"id": "user-1", "email": "owner@vertex.local", "fullName": "เจ้าของ",
			"emailVerified": true, "roles": []string{"USER"},
		})
	})

	mux.HandleFunc("/api/v1/auth/admin/users", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.authCalls, 1)
		writeJSON(w, []map[string]any{
			{"id": "user-1", "email": "owner@vertex.local", "fullName": "เจ้าของ", "roles": []string{"USER"}},
			{"id": "user-2", "email": "cg@vertex.local", "fullName": "ผู้ดูแล", "roles": []string{"USER"}},
			{"id": "user-3", "email": "cg3@vertex.local", "fullName": "ผู้ดูแลสาม", "roles": []string{"USER"}},
		})
	})

	mux.HandleFunc("/api/v1/pets", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.petCalls, 1)
		f.lastAuthHeader.Store(r.Header.Get("Authorization"))
		writeJSON(w, []map[string]any{samplePet()})
	})

	// ต้องมีเส้นนี้ ไม่งั้น query pet(id:) จะได้ 404 แล้ว resolver คืน null เงียบๆ
	mux.HandleFunc("/api/v1/pets/{id}", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&f.petCalls, 1)
		f.lastAuthHeader.Store(r.Header.Get("Authorization"))
		writeJSON(w, samplePet())
	})

	mux.HandleFunc("/api/v1/admin/pets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, []map[string]any{samplePet()})
	})

	mux.HandleFunc("/api/v1/admin/events", func(w http.ResponseWriter, r *http.Request) {
		if f.eventFails {
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, map[string]any{"error": "event-service ล่ม", "requestId": "req-x"})
			return
		}
		writeJSON(w, map[string]any{"data": []any{}, "total": 0, "limit": 0, "offset": 0})
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func samplePet() map[string]any {
	return map[string]any{
		"id": "pet-1", "ownerId": "user-1", "ownerUsername": "เจ้าของ",
		"name": "ยูตะ", "species": "CAT", "breed": "Siamese", "colorCode": "#fff",
		"birthDate": "2024-01-15T00:00:00Z", "gender": "Male",
		"isSpayedNeutered": false,
		"hasAvatar":        true,
		"createdAt":        "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
		"caregivers": []map[string]any{
			{"id": "cg-1", "petId": "pet-1", "userId": "user-2",
				"permissions": []map[string]any{{"id": "MANAGE_WATER", "name": "น้ำ", "isActive": true}},
				"createdAt":   "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
			{"id": "cg-2", "petId": "pet-1", "userId": "user-3",
				"permissions": []map[string]any{},
				"createdAt":   "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z"},
		},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func newTestServer(t *testing.T, f *fakeUpstream) http.Handler {
	t.Helper()
	cfg := config.Config{
		PublicBaseURL:      "https://vertex.example",
		UpstreamTimeout:    5 * time.Second,
		MaxQueryComplexity: 1000,
	}
	pets := client.NewPetClient(f.srv.URL, cfg.UpstreamTimeout)
	auth := client.NewAuthClient(f.srv.URL, cfg.UpstreamTimeout)
	events := client.NewEventClient(f.srv.URL, cfg.UpstreamTimeout)

	r := &graph.Resolver{PetSvc: pets, AuthSvc: auth, EventSvc: events, Cfg: cfg}
	return withRequestContext(newGraphQLServer(r, cfg), pets, auth)
}

type gqlResp struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Path       []any          `json:"path"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

func execute(t *testing.T, h http.Handler, query string, token string) gqlResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query})
	req := httptest.NewRequest("POST", "/graphql", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var out gqlResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("อ่าน response ไม่ได้: %v\nbody=%s", err, rec.Body.String())
	}
	return out
}

// TestHomeQuery พิสูจน์ว่าหน้า Home ได้สิ่งที่ต้องการโดยไม่ต้องโหลดอะไรเกิน
func TestHomeQuery(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `query Home { viewer { fullName petCount } }`, "tok-123")
	if len(resp.Errors) > 0 {
		t.Fatalf("ไม่ควรมี error: %+v", resp.Errors)
	}

	var got struct {
		Viewer struct {
			FullName string `json:"fullName"`
			PetCount int    `json:"petCount"`
		} `json:"viewer"`
	}
	mustUnmarshal(t, resp.Data, &got)

	if got.Viewer.FullName != "เจ้าของ" {
		t.Errorf("fullName = %q ต้องเป็น เจ้าของ", got.Viewer.FullName)
	}
	if got.Viewer.PetCount != 1 {
		t.Errorf("petCount = %d ต้องเป็น 1", got.Viewer.PetCount)
	}
}

// TestForwardsCallerToken ตรวจว่า JWT ของผู้เรียกถูกส่งต่อไปให้ service ปลายทาง
//
// ถ้าข้อนี้พัง แปลว่า BFF กำลังเรียก service ด้วยสิทธิ์ของตัวเอง
// ซึ่งจะทำให้ทุกคนเข้าถึงข้อมูลของคนอื่นได้
func TestForwardsCallerToken(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	execute(t, h, `query Home { viewer { fullName } }`, "tok-abc")

	got, _ := f.lastAuthHeader.Load().(string)
	if got != "Bearer tok-abc" {
		t.Fatalf("Authorization ที่ upstream ได้รับ = %q ต้องเป็น %q", got, "Bearer tok-abc")
	}
}

// TestAnonymousOperationRejected — ตาม VT-100 ทุก operation ต้องมีชื่อ
// ไม่งั้นกรองใน Kibana ไม่ได้ว่า request มาจากหน้าไหน
func TestAnonymousOperationRejected(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `{ viewer { fullName } }`, "tok-123")
	if len(resp.Errors) == 0 {
		t.Fatal("query ที่ไม่มีชื่อต้องถูกปฏิเสธ")
	}
	if !strings.Contains(resp.Errors[0].Message, "ต้องตั้งชื่อ") {
		t.Errorf("ข้อความ error ไม่ได้บอกสาเหตุ: %q", resp.Errors[0].Message)
	}
}

// TestCaregiverNamesResolveWithoutNPlusOne คือเหตุผลหลักที่ทำ BFF
//
// เดิม client ดึงตาราง user ทั้งระบบมาเองเพื่อแปลง id เป็นชื่อ
// ที่นี่ต้องได้ชื่อครบ โดยยิงไป auth-service แค่ครั้งเดียวไม่ว่าจะมีผู้ดูแลกี่คน
func TestCaregiverNamesResolveWithoutNPlusOne(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `query PetCaregivers {
		pet(id: "pet-1") { caregivers { user { id fullName } } }
	}`, "tok-123")
	if len(resp.Errors) > 0 {
		t.Fatalf("ไม่ควรมี error: %+v", resp.Errors)
	}

	var got struct {
		Pet struct {
			Caregivers []struct {
				User struct {
					ID       string `json:"id"`
					FullName string `json:"fullName"`
				} `json:"user"`
			} `json:"caregivers"`
		} `json:"pet"`
	}
	mustUnmarshal(t, resp.Data, &got)

	if len(got.Pet.Caregivers) != 2 {
		t.Fatalf("ได้ผู้ดูแล %d คน ต้องเป็น 2", len(got.Pet.Caregivers))
	}
	names := map[string]string{}
	for _, c := range got.Pet.Caregivers {
		names[c.User.ID] = c.User.FullName
	}
	if names["user-2"] != "ผู้ดูแล" || names["user-3"] != "ผู้ดูแลสาม" {
		t.Errorf("ชื่อผู้ดูแลไม่ครบ: %+v", names)
	}

	if n := atomic.LoadInt32(&f.authCalls); n != 1 {
		t.Errorf("ยิงไป auth-service %d ครั้ง ต้องเป็น 1 ครั้งเท่านั้น (N+1)", n)
	}
}

// TestAvatarIsUrlNotBase64 — รูปต้องเป็น URL ที่ชี้กลับไป REST เดิม
// การส่ง base64 คือปัญหาที่ GraphQL ตั้งใจมาแก้
func TestAvatarIsUrlNotBase64(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `query MyCats { viewer { pets { hasAvatar avatarUrl } } }`, "tok-123")
	if len(resp.Errors) > 0 {
		t.Fatalf("ไม่ควรมี error: %+v", resp.Errors)
	}
	if strings.Contains(string(resp.Data), "avatarData") {
		t.Error("response ไม่ควรมี avatarData เลย")
	}
	want := "https://vertex.example/api/v1/pets/pet-1/avatar"
	if !strings.Contains(string(resp.Data), want) {
		t.Errorf("avatarUrl ต้องเป็น %s แต่ได้ %s", want, resp.Data)
	}
}

// TestPartialErrorKeepsWorkingCards คือเหตุผลที่ field ของ admin เป็น nullable
//
// event-service ล่มต้องไม่ทำให้การ์ดอื่นบนหน้า Dashboard หายไปด้วย
func TestPartialErrorKeepsWorkingCards(t *testing.T) {
	f := newFakeUpstream(t)
	f.eventFails = true
	h := newTestServer(t, f)

	resp := execute(t, h, `query AdminDashboard {
		admin {
			users(first: 5) { totalCount }
			events(first: 5) { totalCount }
		}
	}`, "tok-123")

	if len(resp.Errors) == 0 {
		t.Fatal("ต้องมี error ของ events")
	}

	var got struct {
		Admin *struct {
			Users *struct {
				TotalCount *int `json:"totalCount"`
			} `json:"users"`
			Events *struct {
				TotalCount *int `json:"totalCount"`
			} `json:"events"`
		} `json:"admin"`
	}
	mustUnmarshal(t, resp.Data, &got)

	if got.Admin == nil {
		t.Fatal("admin ทั้งก้อนกลายเป็น null — การ์ดที่สำเร็จหายไปด้วย")
	}
	if got.Admin.Users == nil || got.Admin.Users.TotalCount == nil {
		t.Error("การ์ด users ต้องยังแสดงผลได้แม้ events จะพัง")
	}
	if got.Admin.Events != nil {
		t.Error("การ์ด events ต้องเป็น null เพราะ upstream ล่ม")
	}

	// error ต้องบอกได้ว่าพังตรงไหน เพื่อให้ UI แสดงเฉพาะการ์ดนั้น
	found := false
	for _, e := range resp.Errors {
		if len(e.Path) == 2 && e.Path[0] == "admin" && e.Path[1] == "events" {
			found = true
			if e.Extensions["code"] != "UPSTREAM_ERROR" {
				t.Errorf("code = %v ต้องเป็น UPSTREAM_ERROR", e.Extensions["code"])
			}
		}
	}
	if !found {
		t.Errorf("errors ต้องระบุ path admin.events: %+v", resp.Errors)
	}
}

// TestAgeLabelComputedOnServer — ตรรกะที่เคยซ้ำอยู่สอง client ย้ายมาที่เดียว
func TestAgeLabelComputedOnServer(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `query MyCats { viewer { pets { ageLabel } } }`, "tok-123")
	if len(resp.Errors) > 0 {
		t.Fatalf("ไม่ควรมี error: %+v", resp.Errors)
	}
	if !strings.Contains(string(resp.Data), "ปี") {
		t.Errorf("ageLabel ต้องเป็นข้อความอ่านง่าย ได้: %s", resp.Data)
	}
}

// TestViewerPermissionsFromServer — สิทธิ์ต้องมาจาก server ไม่ใช่ client เดาเอง
func TestViewerPermissionsFromServer(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServer(t, f)

	resp := execute(t, h, `query CatProfile {
		pet(id: "pet-1") { viewerPermissions { isOwner canEditProfile canDelete } }
	}`, "tok-123")
	if len(resp.Errors) > 0 {
		t.Fatalf("ไม่ควรมี error: %+v", resp.Errors)
	}

	var got struct {
		Pet struct {
			VP struct {
				IsOwner        bool `json:"isOwner"`
				CanEditProfile bool `json:"canEditProfile"`
				CanDelete      bool `json:"canDelete"`
			} `json:"viewerPermissions"`
		} `json:"pet"`
	}
	mustUnmarshal(t, resp.Data, &got)

	// user-1 เป็นเจ้าของสัตว์เลี้ยงตัวนี้
	if !got.Pet.VP.IsOwner || !got.Pet.VP.CanEditProfile || !got.Pet.VP.CanDelete {
		t.Errorf("เจ้าของต้องได้สิทธิ์ครบ: %+v", got.Pet.VP)
	}
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("อ่าน data ไม่ได้: %v\nraw=%s", err, raw)
	}
}
