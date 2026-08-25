// Package client เรียก REST ของ service ข้างใน
//
// หลักที่ห้ามละเมิด: ส่ง JWT ของผู้เรียกต่อไปให้ service ปลายทางเสมอ
// ไม่ให้ BFF ใช้สิทธิ์ของตัวเอง มิฉะนั้น BFF จะกลายเป็นช่องข้ามสิทธิ์
// การตรวจสิทธิ์จริงยังอยู่ที่ชั้น service ตามที่ pet-service ตั้งใจไว้
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ctxKey แยกชนิดเพื่อไม่ให้ชนกับ key ของแพ็กเกจอื่นใน context เดียวกัน
type ctxKey struct{ name string }

var tokenKey = ctxKey{"bearer"}

// WithToken ฝัง JWT ดิบของผู้เรียกลง context
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

func TokenFrom(ctx context.Context) string {
	v, _ := ctx.Value(tokenKey).(string)
	return v
}

// Error คือความผิดพลาดจาก service ปลายทางที่แปลงเป็นรูปแบบเดียวแล้ว
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *Error) Error() string { return e.Message }

// codeFor แปลง HTTP status เป็นรหัสที่ client แยกกรณีได้โดยไม่ต้องอ่านข้อความ
//
// ข้อความจาก service เป็นภาษาไทยและเปลี่ยนได้ตลอด การให้ client ไปจับคู่
// ข้อความเองจะพังเงียบๆ วันที่มีคนแก้คำ
func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "VALIDATION"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "FORBIDDEN"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "CONFLICT"
	default:
		if status >= 500 {
			return "UPSTREAM_ERROR"
		}
		return "UNKNOWN"
	}
}

type HTTP struct {
	base string
	c    *http.Client
}

func NewHTTP(base string, timeout time.Duration) *HTTP {
	return &HTTP{
		base: strings.TrimRight(base, "/"),
		c:    &http.Client{Timeout: timeout},
	}
}

// Do ยิงหนึ่ง request แล้วแปลงผลลงใน out
//
// out เป็น nil ได้ถ้าไม่สนใจ body (เช่น DELETE ที่ตอบ 204)
func (h *HTTP) Do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	full := h.base + path
	if len(query) > 0 {
		full += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("แปลง request body เป็น JSON ไม่ได้: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, full, reader)
	if err != nil {
		return fmt.Errorf("สร้าง request ไม่ได้: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	// ส่ง JWT ของผู้เรียกต่อ — จุดสำคัญของทั้งไฟล์นี้
	if tok := TokenFrom(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if rid := RequestIDFrom(ctx); rid != "" {
		req.Header.Set("X-Request-Id", rid)
	}

	resp, err := h.c.Do(req)
	if err != nil {
		return &Error{Status: 0, Code: "UPSTREAM_UNREACHABLE", Message: "เรียก service ปลายทางไม่สำเร็จ: " + err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var e struct {
			Error     string `json:"error"`
			RequestID string `json:"requestId"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Error
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &Error{
			Status:    resp.StatusCode,
			Code:      codeFor(resp.StatusCode),
			Message:   msg,
			RequestID: e.RequestID,
		}
	}

	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("อ่าน response ของ %s %s ไม่ได้: %w", method, path, err)
	}
	return nil
}

var requestIDKey = ctxKey{"requestID"}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}
