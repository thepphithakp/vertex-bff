package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGemini ตั้ง endpoint ให้ชี้ไปที่ server ปลอม เพื่อจะได้ทดสอบได้
// โดยไม่ต้องมี API key จริงและไม่ต้องยิงออกอินเทอร์เน็ตตอนรัน CI
func fakeGemini(t *testing.T, h http.HandlerFunc) (*Gemini, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	g := NewGemini("test-key", []string{"gemini-test"}, 5*time.Second)
	g.endpoint = srv.URL
	return g, srv
}

func okResponse(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"` + text + `"}]}}]}`))
	}
}

func sampleFacts() WaterFacts {
	target := 200
	weight := 4.0
	return WaterFacts{
		PetName:     "มะลิ",
		Species:     "CAT",
		AgeLabel:    "2 ปี",
		WeightKg:    &weight,
		TodayMl:     120,
		TargetMl:    &target,
		AvgMlPerDay: 150,
		DaysInRange: 7,
	}
}

func TestNoAPIKeyReportsDisabledInsteadOfFailing(t *testing.T) {
	s := NewService(NewGemini("", []string{"gemini-test"}, time.Second), time.Hour)

	if s.Enabled() {
		t.Fatal("ไม่มี key แล้วยังบอกว่าเปิดใช้อยู่")
	}
	_, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != ErrDisabled {
		t.Fatalf("อยากได้ ErrDisabled แต่ได้ %v", err)
	}
}

// จุดสำคัญของ free tier — เปิดหน้าซ้ำต้องไม่ยิงใหม่
func TestSameFactsServeFromCache(t *testing.T) {
	calls := 0
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		okResponse("ดื่มน้ำได้ดี")(w, r)
	})
	s := NewService(g, time.Hour)

	first, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != nil {
		t.Fatalf("รอบแรกพัง: %v", err)
	}
	if first.Cached {
		t.Error("รอบแรกไม่ควรมาจาก cache")
	}

	second, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != nil {
		t.Fatalf("รอบสองพัง: %v", err)
	}
	if !second.Cached {
		t.Error("รอบสองควรมาจาก cache")
	}
	if calls != 1 {
		t.Fatalf("ยิงไป Gemini %d ครั้ง ควรยิงครั้งเดียว — free tier มีเพดานต่อนาที", calls)
	}
}

// บันทึกน้ำเพิ่มทีละ 5 ml ไม่ควรทำให้ต้องยิงใหม่ทุกครั้ง
func TestTinyIntakeChangeKeepsCache(t *testing.T) {
	calls := 0
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	f := sampleFacts()
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	f.TodayMl += 5
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("ยิง %d ครั้ง — ขยับ 5 ml ไม่ควรทำให้ generate ใหม่", calls)
	}
}

func TestRealChangeRegenerates(t *testing.T) {
	calls := 0
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	f := sampleFacts()
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	f.TodayMl += 80
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("ยิง %d ครั้ง — ดื่มเพิ่ม 80 ml เปลี่ยนเนื้อหาคำแนะนำ ต้อง generate ใหม่", calls)
	}
}

func TestExpiredCacheRegenerates(t *testing.T) {
	calls := 0
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	now := time.Now()
	s.now = func() time.Time { return now }

	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("ยิง %d ครั้ง — เกิน TTL แล้วต้อง generate ใหม่", calls)
	}
}

// 429 ต้องแยกออกจาก error อื่น ไม่งั้นดู log แล้วนึกว่า key ผิด
func TestQuotaExceededIsItsOwnError(t *testing.T) {
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"Quota exceeded"}}`))
	})
	s := NewService(g, time.Hour)

	_, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != ErrQuota {
		t.Fatalf("อยากได้ ErrQuota แต่ได้ %v", err)
	}
}

func TestAPIKeyGoesInHeaderNotURL(t *testing.T) {
	var gotHeader, gotURL string
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-goog-api-key")
		gotURL = r.URL.String()
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err != nil {
		t.Fatal(err)
	}
	if gotHeader != "test-key" {
		t.Errorf("ไม่ได้ส่ง key ทาง header: %q", gotHeader)
	}
	// key ใน query string จะไปโผล่ใน access log ของ proxy ระหว่างทาง
	if strings.Contains(gotURL, "test-key") {
		t.Errorf("key ไปโผล่ใน URL: %s", gotURL)
	}
}

// regression ของบั๊กที่เจอตอนทดสอบกับ Gemini จริง — เปิด thinking ไว้แล้ว
// โทเคนที่ใช้คิดกิน MaxOutputTokens จนคำตอบถูกตัดกลางประโยค
func TestThinkingIsTurnedOff(t *testing.T) {
	var body []byte
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err != nil {
		t.Fatal(err)
	}

	var req struct {
		GenerationConfig struct {
			MaxOutputTokens int `json:"maxOutputTokens"`
			ThinkingConfig  struct {
				ThinkingBudget int `json:"thinkingBudget"`
			} `json:"thinkingConfig"`
		} `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("อ่าน request ไม่ได้: %v", err)
	}
	if req.GenerationConfig.ThinkingConfig.ThinkingBudget != 0 {
		t.Errorf("thinkingBudget = %d ต้องเป็น 0 ไม่งั้นคำตอบจะถูกตัดกลางประโยค",
			req.GenerationConfig.ThinkingConfig.ThinkingBudget)
	}
	if req.GenerationConfig.MaxOutputTokens < 512 {
		t.Errorf("maxOutputTokens = %d น้อยไปสำหรับภาษาไทย",
			req.GenerationConfig.MaxOutputTokens)
	}
}

// สิ่งที่ออกไปนอกคลัสเตอร์ต้องมีแค่ที่ตั้งใจ — ห้ามมี id ของ user หรือ pet
func TestPromptCarriesNoIdentifiers(t *testing.T) {
	var body string
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		okResponse("ok")(w, r)
	})
	s := NewService(g, time.Hour)

	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"@", "uuid", "-4", "petId", "userId"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("prompt มี %q ติดไปด้วย: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "ห้ามวินิจฉัยโรค") {
		t.Error("system prompt ต้องห้ามโมเดลวินิจฉัยโรค")
	}
}

// ---------------------------------------------------------------------------
// การสลับโมเดลอัตโนมัติเมื่อโดนเพดาน (โควตาของ free tier แยกก้อนต่อโมเดล)
// ---------------------------------------------------------------------------

func quotaResponse(retryDelay string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		body := `{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"`
		if retryDelay != "" {
			body += `,"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"` + retryDelay + `"}]`
		}
		body += `}}`
		_, _ = w.Write([]byte(body))
	}
}

// เส้นทางหลักของ feature นี้ — ตัวหลักหมดโควตาแล้วต้องได้คำตอบจากตัวสำรอง
// ไม่ใช่ตกไปใช้ข้อความ rule-based ทั้งที่ยังมีโมเดลอื่นเหลืออยู่
func TestFallsBackToNextModelOnQuota(t *testing.T) {
	var tried []string
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		model := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/models/"), ":generateContent")
		tried = append(tried, model)
		if model == "primary" {
			quotaResponse("26s")(w, r)
			return
		}
		okResponse("จากตัวสำรอง")(w, r)
	})
	g.models = []string{"primary", "backup"}
	s := NewService(g, time.Hour)

	ins, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != nil {
		t.Fatalf("ควรได้คำตอบจากตัวสำรอง แต่ได้ error: %v", err)
	}
	if ins.Model != "backup" {
		t.Errorf("model ที่ตอบ = %q ควรเป็น backup", ins.Model)
	}
	if len(tried) != 2 || tried[0] != "primary" {
		t.Errorf("ลำดับที่ลอง = %v ควรลองตัวหลักก่อน", tried)
	}
}

// ตัวที่เพิ่งโดนเพดานต้องถูกข้ามไปเลย ไม่ใช่ยิงให้โดน 429 ซ้ำทุกครั้ง
func TestSkipsModelStillCoolingDown(t *testing.T) {
	var tried []string
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		model := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/models/"), ":generateContent")
		tried = append(tried, model)
		if model == "primary" {
			quotaResponse("60s")(w, r)
			return
		}
		okResponse("ok")(w, r)
	})
	g.models = []string{"primary", "backup"}
	s := NewService(g, time.Hour)

	f := sampleFacts()
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	// เปลี่ยนตัวเลขให้ cache ไม่ช่วย จะได้ยิงจริงอีกรอบ
	f.TodayMl += 80
	if _, err := s.WaterInsight(context.Background(), f); err != nil {
		t.Fatal(err)
	}

	for _, m := range tried[1:] {
		if m == "primary" {
			t.Fatalf("ยิงซ้ำไปที่ตัวที่ยัง cooldown อยู่: %v", tried)
		}
	}
}

func TestAllModelsExhaustedReportsQuota(t *testing.T) {
	g, _ := fakeGemini(t, quotaResponse("30s"))
	g.models = []string{"a", "b", "c"}
	s := NewService(g, time.Hour)

	_, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != ErrQuota {
		t.Fatalf("อยากได้ ErrQuota แต่ได้ %v", err)
	}
}

// 404 เจอจริงกับ gemini-2.5-flash ที่มีชื่อในรายการ models แต่เรียกไม่ได้
func TestFallsBackWhenModelNotFound(t *testing.T) {
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"message":"model not found"}}`))
			return
		}
		okResponse("ok")(w, r)
	})
	g.models = []string{"missing", "works"}
	s := NewService(g, time.Hour)

	ins, err := s.WaterInsight(context.Background(), sampleFacts())
	if err != nil {
		t.Fatalf("ควรข้ามไปตัวถัดไป แต่ได้ error: %v", err)
	}
	if ins.Model != "works" {
		t.Errorf("model ที่ตอบ = %q", ins.Model)
	}
}

// prompt ที่ถูกบล็อกจะโดนเหมือนกันทุกโมเดล ยิงต่อก็เปลืองโควตาเปล่า
func TestBlockedPromptDoesNotBurnOtherModels(t *testing.T) {
	calls := 0
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"promptFeedback":{"blockReason":"SAFETY"}}`))
	})
	g.models = []string{"a", "b", "c"}
	s := NewService(g, time.Hour)

	if _, err := s.WaterInsight(context.Background(), sampleFacts()); err == nil {
		t.Fatal("ควรได้ error")
	}
	if calls != 1 {
		t.Fatalf("ยิงไป %d ครั้ง ควรหยุดที่ตัวแรก", calls)
	}
}
