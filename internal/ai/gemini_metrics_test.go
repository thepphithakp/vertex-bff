package ai

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// counterValue อ่านค่าปัจจุบันของ counter ตัวหนึ่งจาก default registry
//
// อ่านจาก registry จริงแทนการ mock เพื่อพิสูจน์ว่า metric ถูก register
// และโผล่ที่ /metrics จริง — ถ้าลืม MustRegister เทสจะจับได้ตรงนี้
func counterValue(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather ไม่สำเร็จ: %v", err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if matchLabels(m, labels) {
				return m.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func matchLabels(m *dto.Metric, want map[string]string) bool {
	got := map[string]string{}
	for _, l := range m.GetLabel() {
		got[l.GetName()] = l.GetValue()
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// TestGeminiRequestCountedOKOnSuccess — เส้นทางปกติต้องนับเป็น ok
func TestGeminiRequestCountedOKOnSuccess(t *testing.T) {
	before := counterValue(t, "gemini_requests_total", map[string]string{"result": "ok"})

	g, _ := fakeGemini(t, okResponse("ปกติดี"))
	if _, _, err := g.Generate(context.Background(), "sys", "user"); err != nil {
		t.Fatalf("ไม่ควร error: %v", err)
	}

	after := counterValue(t, "gemini_requests_total", map[string]string{"result": "ok"})
	if after != before+1 {
		t.Errorf("gemini_requests_total{result=ok} ควรเพิ่ม 1 แต่ได้ %v → %v", before, after)
	}
}

// TestGeminiDisabledNotCountedAsError เป็นข้อกำหนดหลักของ VT-134
//
// ไม่ได้ตั้ง API key เป็นสภาพที่ตั้งใจ ไม่ใช่ความผิดพลาด ถ้ายุบไปรวมกับ error
// alert จะเด้งตลอดเวลาบน environment ที่ไม่ได้เปิดฟีเจอร์นี้ แล้วคนจะเลิกอ่าน
func TestGeminiDisabledNotCountedAsError(t *testing.T) {
	beforeDisabled := counterValue(t, "gemini_requests_total", map[string]string{"result": "disabled"})
	beforeError := counterValue(t, "gemini_requests_total", map[string]string{"result": "error"})

	g := NewGemini("", []string{"gemini-test"}, time.Second) // ไม่มี key
	if _, _, err := g.Generate(context.Background(), "sys", "user"); err != ErrDisabled {
		t.Fatalf("ควรได้ ErrDisabled แต่ได้ %v", err)
	}

	if got := counterValue(t, "gemini_requests_total", map[string]string{"result": "disabled"}); got != beforeDisabled+1 {
		t.Errorf("result=disabled ควรเพิ่ม 1 แต่ได้ %v → %v", beforeDisabled, got)
	}
	if got := counterValue(t, "gemini_requests_total", map[string]string{"result": "error"}); got != beforeError {
		t.Errorf("result=error ต้องไม่ขยับเลย แต่ได้ %v → %v", beforeError, got)
	}
}

// TestFallbackCountsEveryModelButOneRequest เป็นเหตุผลที่ต้องมีสอง metric
//
// โมเดลแรกพัง (503) แล้วตัวที่สองสำเร็จ — ผู้ใช้ยังได้คำวิเคราะห์ตามปกติ
// จึงต้องนับ request เดียวว่า ok แต่ต้องเห็นด้วยว่าโมเดลแรกใช้ไม่ได้
// ถ้ามีแต่ตัวรวม อาการ "fallback ทำงานหนัก" จะมองไม่เห็นจนตัวสุดท้ายพังตาม
func TestFallbackCountsEveryModelButOneRequest(t *testing.T) {
	beforeOK := counterValue(t, "gemini_requests_total", map[string]string{"result": "ok"})
	beforeBad := counterValue(t, "gemini_model_attempts_total",
		map[string]string{"model": "model-a", "result": "unavailable"})

	var seen int
	g, _ := fakeGemini(t, func(w http.ResponseWriter, r *http.Request) {
		seen++
		if seen == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		okResponse("มาจากตัวสำรอง")(w, r)
	})
	g.models = []string{"model-a", "model-b"}

	_, used, err := g.Generate(context.Background(), "sys", "user")
	if err != nil {
		t.Fatalf("ตัวสำรองควรรับช่วงได้: %v", err)
	}
	if used != "model-b" {
		t.Errorf("ควรได้คำตอบจาก model-b แต่ได้ %q", used)
	}

	if got := counterValue(t, "gemini_requests_total", map[string]string{"result": "ok"}); got != beforeOK+1 {
		t.Errorf("request รวมควรนับ ok เพียง 1 ครั้ง แต่ได้ %v → %v", beforeOK, got)
	}
	if got := counterValue(t, "gemini_model_attempts_total",
		map[string]string{"model": "model-a", "result": "unavailable"}); got != beforeBad+1 {
		t.Errorf("model-a ควรถูกนับว่า unavailable แต่ได้ %v → %v", beforeBad, got)
	}
	if got := counterValue(t, "gemini_model_attempts_total",
		map[string]string{"model": "model-b", "result": "ok"}); got < 1 {
		t.Errorf("model-b ควรถูกนับว่า ok แต่ได้ %v", got)
	}
}
