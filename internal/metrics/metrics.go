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
)

func init() {
	prometheus.MustRegister(graphqlOperations, graphqlDuration, graphqlRejected, graphqlComplexity)
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
