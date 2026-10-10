package graph

import (
	"sort"
	"strings"
	"time"

	"github.com/vertex/bff/internal/adapter"
	"github.com/vertex/bff/internal/graph/model"
)

// -----------------------------------------------------------------------------
// สรุปรายวัน
// -----------------------------------------------------------------------------
//
// แยกออกมาจาก mapping.go เพราะเป็นคนละเรื่องกัน — mapping.go แปลง DTO ของ
// REST เป็น model ของ GraphQL ตรงไปตรงมา ส่วนที่นี่คือ aggregation math จริง
// (bucket รายวัน, ค่าเฉลี่ย, เป้าหมายจากน้ำหนัก) ที่ซับซ้อนกว่าการแปลงรูปร่าง
// เฉยๆ มากพอที่ควรมีไฟล์ของตัวเอง

// dayKey ตัดเวลาออกให้เหลือแค่วัน ใน timezone ที่กำหนด ใช้เป็น key ของ bucket
//
// ต้องแปลง location ก่อนเสมอ ห้ามใช้ t.Location() ของแต่ละค่า
//
// time.Time ที่เอาไปเป็น key ของ map จะถือว่าเท่ากันก็ต่อเมื่อทั้งเวลาและ
// location ตรงกัน วันที่ 20 ส.ค. เที่ยงคืน UTC กับวันที่ 20 ส.ค. เที่ยงคืน +07:00
// เป็นคนละ key แม้จะเป็น "วันเดียวกัน" ในสายตาผู้ใช้
//
// log ที่ parse มาจาก JSON เป็น UTC ส่วน from มาพร้อม offset ของเครื่องผู้เรียก
// ตอนที่เขียนครั้งแรกใช้ t.Location() ทั้งสองฝั่ง ผลคือ key คนละชุดกันสนิท
// bucket รายวันจึงเป็น 0 ทั้งหมด ทั้งที่ยอดรวมกับค่าเฉลี่ยถูกต้อง
// (หน้า Analytics ของ iOS ขึ้นตัวเลขถูกแต่กราฟว่างเปล่า — VT-102)
func dayKey(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// eachDay ไล่ทุกวันตั้งแต่ from ถึง to
//
// นับวันตาม timezone ของ from เพราะนั่นคือ "วัน" ในปฏิทินของผู้ใช้ที่ถาม
// ต้องเติมวันที่ไม่มีข้อมูลเป็น 0 ด้วย ไม่งั้นกราฟจะขาดช่วงและอ่านผิด
func eachDay(from, to time.Time, fn func(time.Time)) {
	loc := from.Location()
	for d := dayKey(from, loc); !d.After(dayKey(to, loc)); d = d.AddDate(0, 0, 1) {
		fn(d)
	}
}

func daysBetween(from, to time.Time) float64 {
	n := 0
	eachDay(from, to, func(time.Time) { n++ })
	if n == 0 {
		return 1
	}
	return float64(n)
}

func buildLitterSummary(logs []adapter.LitterLog, from, to time.Time) *model.LitterSummary {
	loc := from.Location()
	poop := map[time.Time]int{}
	pee := map[time.Time]int{}
	totalPoop, totalPee := 0, 0

	for _, l := range logs {
		if !l.IsActive {
			continue
		}
		k := dayKey(l.Date, loc)
		switch strings.ToLower(l.Type) {
		case "poop":
			poop[k] += l.Amount
			totalPoop += l.Amount
		case "pee":
			pee[k] += l.Amount
			totalPee += l.Amount
		}
	}

	var daily []*model.LitterDailyBucket
	eachDay(from, to, func(d time.Time) {
		daily = append(daily, &model.LitterDailyBucket{Date: d, Poop: poop[d], Pee: pee[d]})
	})
	sort.Slice(daily, func(i, j int) bool { return daily[i].Date.Before(daily[j].Date) })

	n := daysBetween(from, to)
	return &model.LitterSummary{
		From: from, To: to,
		TotalPoop: totalPoop, TotalPee: totalPee,
		AvgPoopPerDay: float64(totalPoop) / n,
		AvgPeePerDay:  float64(totalPee) / n,
		Daily:         daily,
	}
}

// waterTargetMlPerKg คือเกณฑ์ที่ client ใช้อยู่เดิม (50 ml ต่อกิโลกรัมต่อวัน)
const waterTargetMlPerKg = 50.0

func buildWaterSummary(logs []adapter.WaterLog, from, to time.Time, weightKg *float64) *model.WaterSummary {
	loc := from.Location()
	ml := map[time.Time]int{}
	total := 0
	for _, l := range logs {
		if !l.IsActive {
			continue
		}
		ml[dayKey(l.Date, loc)] += l.Amount
		total += l.Amount
	}

	var daily []*model.WaterDailyBucket
	eachDay(from, to, func(d time.Time) {
		daily = append(daily, &model.WaterDailyBucket{Date: d, Ml: ml[d]})
	})
	sort.Slice(daily, func(i, j int) bool { return daily[i].Date.Before(daily[j].Date) })

	s := &model.WaterSummary{
		From: from, To: to,
		TotalMl:     total,
		AvgMlPerDay: float64(total) / daysBetween(from, to),
		Daily:       daily,
	}
	// เป้าหมายคำนวณได้ต่อเมื่อรู้น้ำหนักจริง
	// client เดิมเดาเป็น 4 กก. เมื่อไม่รู้ แล้วแสดงเป้าหมายที่ไม่จริง
	// การคืน null บอกตรงๆ ว่าไม่รู้ ดีกว่าแสดงตัวเลขที่ผิด
	if weightKg != nil && *weightKg > 0 {
		t := int(*weightKg * waterTargetMlPerKg)
		s.DailyTargetMl = &t
	}
	return s
}
