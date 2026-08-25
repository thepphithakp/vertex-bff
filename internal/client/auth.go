package client

import (
	"context"
	"net/url"
	"time"
)

type Me struct {
	ID            string   `json:"id"`
	Email         string   `json:"email"`
	FullName      string   `json:"fullName"`
	EmailVerified bool     `json:"emailVerified"`
	Roles         []string `json:"roles"`
}

type User struct {
	ID            string   `json:"id"`
	Email         string   `json:"email"`
	FullName      string   `json:"fullName"`
	EmailVerified bool     `json:"emailVerified"`
	Roles         []string `json:"roles"`
}

type RoleDefinition struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsSystem    bool   `json:"isSystem"`
}

type AuthClient struct{ h *HTTP }

func NewAuthClient(base string, timeout time.Duration) *AuthClient {
	return &AuthClient{h: NewHTTP(base, timeout)}
}

func (c *AuthClient) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.h.Do(ctx, "GET", "/api/v1/auth/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminListUsers ต้องมีสิทธิ์ SUPER_ADMIN
//
// รองรับ q สำหรับค้นหา แต่ยังตัดที่ 200 แถวตายตัวที่ฝั่ง REST
// ไม่มี offset ให้ใช้ — ต้องแก้ auth-service เมื่อ user เกิน 200
func (c *AuthClient) AdminListUsers(ctx context.Context, search string) ([]User, error) {
	q := url.Values{}
	if search != "" {
		q.Set("q", search)
	}
	var out []User
	err := c.h.Do(ctx, "GET", "/api/v1/auth/admin/users", q, nil, &out)
	return out, err
}

func (c *AuthClient) AdminListRoles(ctx context.Context) ([]RoleDefinition, error) {
	var out []RoleDefinition
	err := c.h.Do(ctx, "GET", "/api/v1/auth/admin/roles", nil, nil, &out)
	return out, err
}

func (c *AuthClient) AdminUpdateRoles(ctx context.Context, userID string, roles []string) (*User, error) {
	var out User
	body := map[string]any{"roles": roles}
	if err := c.h.Do(ctx, "PUT", "/api/v1/auth/admin/users/"+userID+"/roles", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UsersByIDs แปลง user id เป็นข้อมูลผู้ใช้ ใช้โดย dataloader
//
// 🔴 ข้อจำกัดที่รู้อยู่: auth-service ไม่มี endpoint ที่ดึง user ตาม id
//
//	endpoint ที่ list user ได้มีแต่ /admin/users ซึ่งเป็น SUPER_ADMIN เท่านั้น
//	ผู้ใช้ทั่วไปจึงแปลง id ของผู้ดูแลร่วมเป็นชื่อไม่ได้เลย
//
//	ที่นี่จึงเรียก /admin/users แล้วกรองเอา ถ้าโดนปฏิเสธสิทธิ์จะคืน user
//	ที่มีแต่ id ให้แทนการทำให้ทั้ง query ล้ม — หน้าจอยังใช้ได้ แค่ไม่เห็นชื่อ
//
//	ทางแก้ที่ถูกคือเพิ่ม endpoint ฝั่ง auth-service ที่ resolve หลาย id พร้อมกัน
//	และเปิดให้ผู้ใช้ที่ใช้สัตว์เลี้ยงตัวเดียวกันเรียกได้
func (c *AuthClient) UsersByIDs(ctx context.Context, ids []string) (map[string]User, error) {
	result := make(map[string]User, len(ids))

	users, err := c.AdminListUsers(ctx, "")
	if err != nil {
		var ce *Error
		if ok := asClientError(err, &ce); ok && (ce.Status == 403 || ce.Status == 401) {
			for _, id := range ids {
				result[id] = User{ID: id}
			}
			return result, nil
		}
		return nil, err
	}

	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	for _, u := range users {
		if _, ok := want[u.ID]; ok {
			result[u.ID] = u
		}
	}
	// id ที่หาไม่เจอ (ถูกลบไปแล้ว) ยังต้องมีคำตอบ ไม่งั้น dataloader จะค้าง
	for _, id := range ids {
		if _, ok := result[id]; !ok {
			result[id] = User{ID: id}
		}
	}
	return result, nil
}

func asClientError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}
