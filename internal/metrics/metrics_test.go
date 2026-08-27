package metrics

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func labelValues(t *testing.T, c prometheus.Collector, label string) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 512)
	c.Collect(ch)
	close(ch)

	out := map[string]float64{}
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		for _, l := range pb.Label {
			if l.GetName() != label {
				continue
			}
			switch {
			case pb.Counter != nil:
				out[l.GetValue()] += pb.Counter.GetValue()
			case pb.Histogram != nil:
				out[l.GetValue()] += float64(pb.Histogram.GetSampleCount())
			}
		}
	}
	return out
}

// ชื่อ operation มาจาก client — ปล่อยไว้คนยิงชื่อมั่วๆ ทำให้ Prometheus
// เก็บ time series ใหม่ทุกชื่อจนหน่วยความจำหมด
func TestOperationLabelsAreCapped(t *testing.T) {
	resetLabels()

	for i := 0; i < maxOperationLabels+20; i++ {
		RecordOperation(fmt.Sprintf("Junk%d", i), 0.01, 10, 0, "")
	}

	if !LabelOverflowed() {
		t.Fatal("ยิงชื่อเกินเพดานแล้วแต่ไม่ได้ถูกยุบ")
	}
	got := labelValues(t, graphqlOperations, "operation")
	if len(got) > maxOperationLabels+1 { // +1 คือ "other"
		t.Errorf("มี label %d ค่า เกินเพดาน %d", len(got), maxOperationLabels)
	}
	if got["other"] == 0 {
		t.Error("ชื่อที่เกินเพดานควรถูกยุบเป็น other")
	}
}

// ชื่อที่เคยเห็นแล้วต้องคงเป็นตัวมันเอง ไม่ถูกยุบตามไปด้วย
func TestKnownOperationsKeepTheirName(t *testing.T) {
	resetLabels()

	RecordOperation("MyCats", 0.01, 10, 0, "")
	for i := 0; i < maxOperationLabels+5; i++ {
		RecordOperation(fmt.Sprintf("Junk%d", i), 0.01, 10, 0, "")
	}
	RecordOperation("MyCats", 0.02, 10, 0, "")

	got := labelValues(t, graphqlOperations, "operation")
	if got["MyCats"] != 2 {
		t.Errorf("MyCats นับได้ %v ควรเป็น 2 — ชื่อที่รู้จักแล้วห้ามถูกยุบ", got["MyCats"])
	}
}

// query ที่โดน guardrail กันคือของที่อยากเห็นที่สุด ต้องนับแยกตามสาเหตุ
func TestRejectedCountsByReason(t *testing.T) {
	resetLabels()

	RecordOperation("Evil", 0.01, 99999, 0, "complexity")
	RecordOperation("Evil", 0.01, 99999, 0, "complexity")
	RecordOperation("", 0.01, 0, 0, "anonymous")

	got := labelValues(t, graphqlRejected, "reason")
	if got["complexity"] != 2 {
		t.Errorf("complexity นับได้ %v ควรเป็น 2", got["complexity"])
	}
	if got["anonymous"] != 1 {
		t.Errorf("anonymous นับได้ %v ควรเป็น 1", got["anonymous"])
	}

	// ของที่ถูกปฏิเสธไม่ควรไปปนกับสถิติเวลาและราคาของ query ที่ทำงานจริง
	dur := labelValues(t, graphqlDuration, "operation")
	if dur["Evil"] != 0 {
		t.Errorf("query ที่ถูกปฏิเสธไม่ควรมีเวลาเข้า histogram (ได้ %v)", dur["Evil"])
	}
}

// partial error เป็นเรื่องปกติของ schema นี้ — ต้องแยกจาก ok ให้เห็นบนกราฟ
func TestPartialErrorsCountAsError(t *testing.T) {
	resetLabels()

	RecordOperation("Dashboard", 0.05, 100, 0, "")
	RecordOperation("Dashboard", 0.05, 100, 2, "")

	ch := make(chan prometheus.Metric, 64)
	graphqlOperations.Collect(ch)
	close(ch)

	results := map[string]float64{}
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		var op, res string
		for _, l := range pb.Label {
			switch l.GetName() {
			case "operation":
				op = l.GetValue()
			case "result":
				res = l.GetValue()
			}
		}
		if op == "Dashboard" {
			results[res] = pb.Counter.GetValue()
		}
	}
	if results["ok"] != 1 || results["error"] != 1 {
		t.Errorf("ควรแยกเป็น ok 1 / error 1 แต่ได้ %v", results)
	}
}

func TestAnonymousHasItsOwnLabel(t *testing.T) {
	resetLabels()
	if got := operationLabel(""); got != "anonymous" {
		t.Errorf("operation ที่ไม่มีชื่อควรเป็น anonymous ได้ %q", got)
	}
	if strings.TrimSpace(operationLabel("MyCats")) != "MyCats" {
		t.Error("ชื่อปกติไม่ควรถูกแปลง")
	}
}
