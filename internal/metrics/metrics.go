// Package metrics เก็บตัวเลขให้ Prometheus มาดึงที่ /metrics
//
// BFF ต่างจาก service อื่นตรงที่ทุก request เป็น POST /graphql เส้นเดียว
// metric ระดับ HTTP จึงบอกอะไรไม่ได้เลยนอกจาก "มี traffic" — สิ่งที่ต้องแยก
// คือ operation ซึ่งเป็นหน่วยเดียวกับที่ใช้ใน log อยู่แล้ว (VT-100)
package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// maxOperationLabels จำกัดจำนวนชื่อ operation ที่ยอมให้กลายเป็น label
//
// ชื่อ operation มาจาก client แปลว่าใครก็ส่งชื่อมั่วๆ มาได้ไม่จำกัด
// ถ้าปล่อยไว้ Prometheus จะเก็บ time series ใหม่ทุกชื่อจนหน่วยความจำหมด
// (cardinality explosion) — แอปจริงมี operation ไม่ถึงยี่สิบตัว
// เกินเพดานนี้แปลว่ามีคนยิงมั่วอยู่ ยุบเป็น "other" ให้หมด
const maxOperationLabels = 50

var (
	graphqlOperations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "graphql_operations_total",
			Help: "จำนวน GraphQL operation แยกตามชื่อและผลลัพธ์",
		},
		[]string{"operation", "result"},
	)

	graphqlDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "graphql_operation_duration_seconds",
			Help:    "เวลาที่ใช้ต่อ operation",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"operation"},
	)

	// graphqlRejected นับ query ที่ถูก guardrail กันไว้
	//
	// ของที่โดนกันคือของที่อยากเห็นที่สุด — ถ้าตัวเลขนี้ขึ้นแปลว่ามีคนยิงถล่ม
	// หรือเพดานตั้งแน่นเกินจนผู้ใช้จริงโดนไปด้วย ซึ่งต้องรีบรู้ทั้งคู่
	graphqlRejected = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "graphql_rejected_total",
			Help: "จำนวน operation ที่ถูกปฏิเสธ แยกตามสาเหตุ",
		},
		[]string{"reason"},
	)

	// graphqlComplexity เก็บราคาของ query ที่ผ่านเข้ามาจริง
	//
	// ใช้ตั้งเพดานจากข้อมูลจริงแทนการเดา — ดู p95 แล้วเทียบกับ MAX_QUERY_COMPLEXITY
	graphqlComplexity = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "graphql_operation_complexity",
			Help:    "ราคาของ operation ที่คำนวณได้",
			Buckets: []float64{10, 50, 100, 250, 500, 1000, 2500, 5000, 10000},
		},
		[]string{"operation"},
	)

	// geminiRequests นับผลรวมของการขอคำวิเคราะห์หนึ่งครั้ง — ตัวที่ใช้ตั้ง alert
	//
	// สนใจแค่ว่า "ผู้ใช้ได้คำวิเคราะห์จริงไหม" ไม่สนว่าเบื้องหลังต้องไล่กี่โมเดล
	// result != "ok" แปลว่าผู้ใช้เห็นข้อความสำรอง rule-based แทนของจริง
	//
	// ก่อนมี metric นี้ อาการนั้นเห็นได้จาก log อย่างเดียว แปลว่าถ้า Gemini
	// ล่มยาวก็ไม่มีใครรู้จนกว่าจะมีคนสังเกตว่าคำวิเคราะห์หน้าตาเหมือนเดิมทุกวัน
	geminiRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gemini_requests_total",
			Help: "จำนวนครั้งที่ขอคำวิเคราะห์จาก Gemini แยกตามผลลัพธ์รวม",
		},
		[]string{"result"},
	)

	// geminiModelAttempts นับรายโมเดล ใช้ตอบว่า "ตัวไหนพัง" ไม่ใช่ "พังไหม"
	//
	// ต่างจาก geminiRequests ตรงที่หนึ่ง request อาจนับหลายครั้งที่นี่
	// เพราะไล่ fallback ทีละตัว — ถ้าตัวแรกหมดโควตาแล้วตัวที่สองสำเร็จ
	// จะได้ quota 1 ครั้งและ ok 1 ครั้ง ส่วน geminiRequests ได้ ok ครั้งเดียว
	//
	// ค่านี้ทำให้เห็นว่า fallback กำลังทำงานหนักอยู่ทั้งที่ผู้ใช้ยังไม่เดือดร้อน
	// ซึ่งเป็นจังหวะที่ควรรู้ก่อนที่โมเดลตัวสุดท้ายจะพังตาม
	//
	// label model ปลอดภัยเรื่อง cardinality เพราะมาจาก GEMINI_MODELS ใน config
	// ไม่ได้มาจาก client
	geminiModelAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gemini_model_attempts_total",
			Help: "จำนวนครั้งที่ยิงไปยังโมเดลแต่ละตัว แยกตามผลลัพธ์",
		},
		[]string{"model", "result"},
	)
)

func init() {
	prometheus.MustRegister(
		graphqlOperations, graphqlDuration, graphqlRejected, graphqlComplexity,
		geminiRequests, geminiModelAttempts,
	)
}

// ผลลัพธ์ที่เป็นไปได้ของการขอคำวิเคราะห์หนึ่งครั้ง
const (
	GeminiOK       = "ok"       // ได้คำวิเคราะห์จริง
	GeminiQuota    = "quota"    // โดนเพดานครบทุกโมเดล หรือทุกตัวยัง cooldown
	GeminiError    = "error"    // พังด้วยเหตุอื่น เช่น prompt ถูกบล็อก
	GeminiDisabled = "disabled" // ยังไม่ได้ตั้ง API key — ตั้งใจ ไม่ใช่ความผิดพลาด
)

// ผลลัพธ์ของการยิงไปยังโมเดลตัวหนึ่ง
const (
	GeminiAttemptOK          = "ok"
	GeminiAttemptQuota       = "quota"       // 429
	GeminiAttemptUnavailable = "unavailable" // 404 หรือ 5xx — ลองตัวถัดไปมีโอกาสรอด
	GeminiAttemptFatal       = "fatal"       // ลองตัวถัดไปก็โดนเหมือนกัน เช่น prompt ถูกบล็อก
	GeminiAttemptCooling     = "cooling"     // ข้ามไปเพราะเพิ่งโดนเพดาน ยังไม่พ้น cooldown
)

// RecordGeminiRequest บันทึกผลรวมของการขอคำวิเคราะห์หนึ่งครั้ง
func RecordGeminiRequest(result string) {
	geminiRequests.WithLabelValues(result).Inc()
}

// RecordGeminiAttempt บันทึกผลของการยิงไปยังโมเดลตัวหนึ่ง
func RecordGeminiAttempt(model, result string) {
	geminiModelAttempts.WithLabelValues(model, result).Inc()
}

var (
	mu       sync.Mutex
	seenOps  = map[string]struct{}{}
	overflow bool
)

// operationLabel กันชื่อ operation ที่ client ส่งมาไม่ให้ทำ label แตก
func operationLabel(name string) string {
	if name == "" {
		return "anonymous"
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := seenOps[name]; ok {
		return name
	}
	if len(seenOps) >= maxOperationLabels {
		overflow = true
		return "other"
	}
	seenOps[name] = struct{}{}
	return name
}

// RecordOperation บันทึกผลของหนึ่ง operation
//
// rejected ว่าง = ผ่าน guardrail มาแล้ว ส่วน errorCount นับจาก errors[] ใน response
// ซึ่งอาจมีได้แม้ operation จะสำเร็จบางส่วน (partial error ตามที่ schema ออกแบบไว้)
func RecordOperation(name string, seconds float64, complexity int, errorCount int, rejected string) {
	op := operationLabel(name)

	if rejected != "" {
		graphqlRejected.WithLabelValues(rejected).Inc()
		graphqlOperations.WithLabelValues(op, "rejected").Inc()
		return
	}

	result := "ok"
	if errorCount > 0 {
		result = "error"
	}
	graphqlOperations.WithLabelValues(op, result).Inc()
	graphqlDuration.WithLabelValues(op).Observe(seconds)
	graphqlComplexity.WithLabelValues(op).Observe(float64(complexity))
}

// LabelOverflowed บอกว่าเคยชนเพดานชื่อ operation แล้วหรือยัง — ใช้ในเทสต์
func LabelOverflowed() bool {
	mu.Lock()
	defer mu.Unlock()
	return overflow
}

// เผื่อไว้ให้เทสต์เริ่มนับใหม่ได้
func resetLabels() {
	mu.Lock()
	defer mu.Unlock()
	seenOps = map[string]struct{}{}
	overflow = false
}
