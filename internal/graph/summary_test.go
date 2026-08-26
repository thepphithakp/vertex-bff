package graph

import (
	"testing"
	"time"

	"github.com/vertex/bff/internal/client"
)

// bangkok คือ timezone ที่ iOS ส่งมาจริง (+07:00)
var bangkok = time.FixedZone("ICT", 7*60*60)

// TestDailyBucketsUseCallerTimezone กันบั๊กที่ bucket รายวันเป็น 0 ทั้งหมด
//
// เคยเกิดขึ้นจริง: หน้า Analytics ของ iOS แสดงยอดรวมกับค่าเฉลี่ยถูกต้อง
// แต่กราฟว่างเปล่า เพราะ key ของ map เป็น time.Time ซึ่งเท่ากันก็ต่อเมื่อ
// location ตรงกันด้วย — log ที่ parse จาก JSON เป็น UTC ส่วน from มาเป็น +07:00
// ทำให้ไม่มี key ไหน match กันเลย
//
// อาการนี้หาเจอยากมากถ้าไม่มี test เพราะตัวเลขบนจอ "ดูถูก" อยู่
func TestDailyBucketsUseCallerTimezone(t *testing.T) {
	// 20 ส.ค. 08:00 ตามเวลาไทย = 01:00 UTC — วันเดียวกันทั้งสอง timezone
	morning := time.Date(2026, 8, 20, 1, 0, 0, 0, time.UTC)
	// 20 ส.ค. 23:30 ตามเวลาไทย = 16:30 UTC — ยังเป็นวันที่ 20 ในไทย
	// แต่ถ้า group ด้วย UTC จะยังอยู่วันที่ 20 เหมือนกัน จึงใส่อีกตัวที่ข้ามวัน
	lateNight := time.Date(2026, 8, 20, 16, 30, 0, 0, time.UTC)
	// 21 ส.ค. 06:00 ไทย = 20 ส.ค. 23:00 UTC — ไทยเป็นวันที่ 21 แต่ UTC เป็นวันที่ 20
	crossesMidnight := time.Date(2026, 8, 20, 23, 0, 0, 0, time.UTC)

	from := time.Date(2026, 8, 20, 0, 0, 0, 0, bangkok)
	to := time.Date(2026, 8, 21, 23, 59, 0, 0, bangkok)

	logs := []client.LitterLog{
		{Date: morning, Type: "Poop", Amount: 1, IsActive: true},
		{Date: lateNight, Type: "Pee", Amount: 2, IsActive: true},
		{Date: crossesMidnight, Type: "Poop", Amount: 5, IsActive: true},
	}

	s := buildLitterSummary(logs, from, to)

	if len(s.Daily) != 2 {
		t.Fatalf("ต้องได้ 2 วัน (20 กับ 21 ส.ค. ตามเวลาไทย) แต่ได้ %d", len(s.Daily))
	}
	if s.Daily[0].Poop != 1 || s.Daily[0].Pee != 2 {
		t.Errorf("วันที่ 20 ส.ค. ต้องได้ poop=1 pee=2 แต่ได้ poop=%d pee=%d",
			s.Daily[0].Poop, s.Daily[0].Pee)
	}
	// ตัวนี้คือหัวใจ: 23:00 UTC เป็นเช้าวันที่ 21 ของผู้ใช้ ต้องไปอยู่ถังวันที่ 21
	if s.Daily[1].Poop != 5 {
		t.Errorf("วันที่ 21 ส.ค. ต้องได้ poop=5 (log เวลา 23:00 UTC = 06:00 ไทยวันถัดไป) แต่ได้ %d",
			s.Daily[1].Poop)
	}

	// ยอดรวมเคยถูกอยู่แล้วตอนที่ bucket พัง — ตรวจไว้กันแก้แล้วไปทำอีกอันพัง
	if s.TotalPoop != 6 || s.TotalPee != 2 {
		t.Errorf("ยอดรวมต้องเป็น poop=6 pee=2 แต่ได้ poop=%d pee=%d", s.TotalPoop, s.TotalPee)
	}

	// ผลรวมของถังต้องเท่ากับยอดรวมเสมอ ไม่งั้นแปลว่ามี log ตกถัง
	sumPoop, sumPee := 0, 0
	for _, d := range s.Daily {
		sumPoop += d.Poop
		sumPee += d.Pee
	}
	if sumPoop != s.TotalPoop || sumPee != s.TotalPee {
		t.Errorf("ผลรวมของถัง (poop=%d pee=%d) ไม่ตรงกับยอดรวม (poop=%d pee=%d)",
			sumPoop, sumPee, s.TotalPoop, s.TotalPee)
	}
}

func TestWaterDailyBucketsUseCallerTimezone(t *testing.T) {
	from := time.Date(2026, 8, 20, 0, 0, 0, 0, bangkok)
	to := time.Date(2026, 8, 20, 23, 59, 0, 0, bangkok)

	logs := []client.WaterLog{
		// 09:30 ไทย
		{Date: time.Date(2026, 8, 20, 2, 30, 0, 0, time.UTC), Amount: 60, IsActive: true},
		// 22:00 ไทย — ยังวันเดียวกันสำหรับผู้ใช้
		{Date: time.Date(2026, 8, 20, 15, 0, 0, 0, time.UTC), Amount: 40, IsActive: true},
	}

	s := buildWaterSummary(logs, from, to, nil)

	if len(s.Daily) != 1 {
		t.Fatalf("ต้องได้ 1 วัน แต่ได้ %d", len(s.Daily))
	}
	if s.Daily[0].Ml != 100 {
		t.Errorf("ถังวันที่ 20 ส.ค. ต้องได้ 100 ml แต่ได้ %d", s.Daily[0].Ml)
	}
	if s.TotalMl != 100 {
		t.Errorf("ยอดรวมต้องเป็น 100 ml แต่ได้ %d", s.TotalMl)
	}
	if s.DailyTargetMl != nil {
		t.Errorf("ไม่รู้น้ำหนักต้องคืน null ไม่ใช่เดาค่า แต่ได้ %d", *s.DailyTargetMl)
	}
}

// TestBucketsStillWorkInUTC กันแก้ทางเดียวจนพังอีกทาง
func TestBucketsStillWorkInUTC(t *testing.T) {
	from := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 20, 23, 59, 0, 0, time.UTC)

	logs := []client.LitterLog{
		{Date: time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC), Type: "Poop", Amount: 1, IsActive: true},
	}

	s := buildLitterSummary(logs, from, to)
	if len(s.Daily) != 1 || s.Daily[0].Poop != 1 {
		t.Fatalf("ถังของ UTC ต้องยังทำงาน แต่ได้ %+v", s.Daily)
	}
}
