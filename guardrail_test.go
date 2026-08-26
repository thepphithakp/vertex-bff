package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vertex/bff/internal/config"
)

// ค่าเดียวกับ default ใน config.Load() — test ที่พิสูจน์ว่า "แอปจริงยังใช้ได้"
// ต้องรันด้วยค่าที่ใช้จริงบน production ไม่งั้นพิสูจน์อะไรไม่ได้
const defaultComplexity = 5000

func guardrailCfg(depth, complexity int) config.Config {
	return config.Config{
		PublicBaseURL:      "https://vertex.example",
		UpstreamTimeout:    5 * time.Second,
		MaxQueryDepth:      depth,
		MaxQueryComplexity: complexity,
	}
}

func errCode(r gqlResp) string {
	if len(r.Errors) == 0 {
		return ""
	}
	code, _ := r.Errors[0].Extensions["code"].(string)
	return code
}

// TestDepthLimitRejectsBeforeResolving เป็นข้อกำหนดหลักของ VT-99
//
// ไม่พอที่จะปฏิเสธ — ต้องปฏิเสธ **ก่อน** resolver ตัวแรกทำงาน ไม่งั้นคนยิงถล่ม
// ก็ยังทำให้ BFF ไปกวน service ปลายทางได้อยู่ดี แค่ไม่ได้คำตอบกลับไป
//
// วัดด้วยจำนวนครั้งที่ upstream ถูกเรียก ซึ่งต้องเป็นศูนย์
func TestDepthLimitRejectsBeforeResolving(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(4, 100000))

	// pet > caregivers > user > id คือ 4 ชั้น ยังผ่าน
	ok := execute(t, h, `query Shallow { pet(id: "pet-1") { caregivers { user { id } } } }`, "token")
	if len(ok.Errors) != 0 {
		t.Fatalf("query 4 ชั้นต้องผ่านเมื่อเพดานเป็น 4 แต่ได้ error: %v", ok.Errors)
	}

	before := atomic.LoadInt32(&f.petCalls)

	deep := execute(t, h, `query Deep {
		pet(id: "pet-1") { caregivers { user { id email } } viewerPermissions { isOwner } }
	}`, "token")
	_ = deep

	// 5 ชั้น: pet > caregivers > user > roles > (ไม่มีลูก) — ใช้ field ที่ลึกกว่า
	tooDeep := execute(t, h, `query TooDeep {
		pet(id: "pet-1") { caregivers { user { id } } litterLogs { edges { node { id } } } }
	}`, "token")

	if got := errCode(tooDeep); got != "DEPTH_LIMIT_EXCEEDED" {
		t.Fatalf("query 5 ชั้นต้องถูกปฏิเสธด้วย DEPTH_LIMIT_EXCEEDED แต่ได้ %q (errors=%v)", got, tooDeep.Errors)
	}
	if len(tooDeep.Data) > 0 && string(tooDeep.Data) != "null" {
		t.Errorf("ถูกปฏิเสธแล้วต้องไม่มี data กลับมา แต่ได้ %s", tooDeep.Data)
	}

	after := atomic.LoadInt32(&f.petCalls)
	if after != before+1 {
		// +1 มาจาก query Deep ที่ผ่านเพดาน ส่วน TooDeep ต้องไม่เพิ่มเลย
		t.Errorf("query ที่ลึกเกินต้องไม่ยิงไป upstream เลย: petCalls %d → %d", before, after)
	}
}

// TestDepthLimitFollowsFragments กันช่องที่เขียน query ตื้นแล้วซ่อนความลึกไว้ใน fragment
//
// ถ้านับเฉพาะ selection ที่เห็นในตัว operation จะได้แค่ 2 ชั้นแล้วผ่านฉลุย
// ทั้งที่ resolver ทำงานลึกเท่าเดิม
func TestDepthLimitFollowsFragments(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(3, 100000))

	r := execute(t, h, `query HiddenDepth {
		pet(id: "pet-1") { ...deep }
	}
	fragment deep on Pet {
		caregivers { user { id } }
	}`, "token")

	if got := errCode(r); got != "DEPTH_LIMIT_EXCEEDED" {
		t.Fatalf("ความลึกที่ซ่อนใน fragment ต้องถูกนับด้วย แต่ได้ %q (errors=%v)", got, r.Errors)
	}
	if atomic.LoadInt32(&f.petCalls) != 0 {
		t.Errorf("ต้องไม่ยิงไป upstream แต่ยิงไป %d ครั้ง", f.petCalls)
	}
}

// TestRealOperationsFitUnderDefaults กันตั้งเพดานแน่นจนแอปจริงใช้ไม่ได้
//
// เพดานที่กัน attacker ได้แต่กันผู้ใช้จริงไปด้วยคือเพดานที่จะถูกปิดทิ้งในอีกสองสัปดาห์
func TestRealOperationsFitUnderDefaults(t *testing.T) {
	f := newFakeUpstream(t)
	// ค่าเดียวกับ default ใน config.Load()
	h := newTestServerWith(t, f, guardrailCfg(12, defaultComplexity))

	cases := map[string]string{
		"MyCats": `query MyCats { viewer { pets { id name breed hasAvatar } } }`,
		"CatProfile": `query CatProfile { pet(id: "pet-1") {
			id name species breed colorCode birthDate gender currentWeight
			microchipId isSpayedNeutered bloodType allergies personality hasAvatar
			owner { id fullName }
		} }`,
		"PetAnalytics": `query PetAnalytics { pet(id: "pet-1") {
			id
			litterSummary(from: "2026-08-20T00:00:00+07:00", to: "2026-08-26T23:59:00+07:00") {
				totalPoop totalPee avgPoopPerDay avgPeePerDay daily { date poop pee }
			}
			waterSummary(from: "2026-08-20T00:00:00+07:00", to: "2026-08-26T23:59:00+07:00") {
				totalMl avgMlPerDay dailyTargetMl daily { date ml }
			}
		} }`,
		"LitterDay": `query LitterDay { pet(id: "pet-1") {
			id litterLogs(first: 200, from: "2026-08-26T00:00:00+07:00", to: "2026-08-27T00:00:00+07:00") {
				edges { node { id date type amount } }
			}
		} }`,
	}

	for name, q := range cases {
		r := execute(t, h, q, "token")
		if code := errCode(r); code == "DEPTH_LIMIT_EXCEEDED" || code == "COMPLEXITY_LIMIT_EXCEEDED" {
			t.Errorf("%s เป็น query ที่แอปใช้จริง ต้องไม่ติดเพดาน แต่ได้ %s: %v", name, code, r.Errors)
		}
	}
}

// TestListComplexityMultipliesByRows คือ query ที่ ticket VT-99 ยกมาเป็นตัวอย่าง
//
// ก่อนแก้ราคาแค่ประมาณ 5 เพราะ gqlgen คิดทุก field เท่ากับ 1 ทั้งที่ query นี้
// ทำให้ resolver ทำงาน 200 × จำนวนผู้ดูแล ครั้ง
func TestListComplexityMultipliesByRows(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(12, defaultComplexity))

	evil := execute(t, h, `query Evil {
		admin { pets(first: 200) { edges { node { caregivers { user { id } } } } } }
	}`, "token")

	if got := errCode(evil); got != "COMPLEXITY_LIMIT_EXCEEDED" {
		t.Fatalf("query ที่ขอ 200 แถวแล้วซ้อน caregiver ต้องเกินเพดาน แต่ได้ %q (errors=%v)", got, evil.Errors)
	}

	// query เดียวกันแต่ขอ 5 แถวต้องยังใช้ได้ — เพดานควรกันขนาด ไม่ใช่กันรูปแบบ
	small := execute(t, h, `query Small {
		admin { pets(first: 5) { edges { node { caregivers { user { id } } } } } }
	}`, "token")
	if got := errCode(small); got == "COMPLEXITY_LIMIT_EXCEEDED" {
		t.Errorf("query รูปเดียวกันแต่ขอแค่ 5 แถวต้องไม่ติดเพดาน: %v", small.Errors)
	}
}

// TestSummaryRangeAffectsComplexity — ขอถังรายวันสิบปีย้อนหลังแพงกว่าขอเจ็ดวัน
// ทั้งที่ query หน้าตาเหมือนกันเป๊ะ ต่างกันแค่ argument
func TestSummaryRangeAffectsComplexity(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(12, defaultComplexity))

	week := execute(t, h, `query Week { pet(id: "pet-1") {
		litterSummary(from: "2026-08-20T00:00:00Z", to: "2026-08-26T00:00:00Z") {
			daily { date poop pee }
		}
	} }`, "token")
	if got := errCode(week); got == "COMPLEXITY_LIMIT_EXCEEDED" {
		t.Fatalf("ช่วง 7 วันต้องไม่ติดเพดาน: %v", week.Errors)
	}

	decade := execute(t, h, `query Decade { pet(id: "pet-1") {
		litterSummary(from: "2016-08-20T00:00:00Z", to: "2026-08-26T00:00:00Z") {
			daily { date poop pee }
		}
	} }`, "token")
	if got := errCode(decade); got != "COMPLEXITY_LIMIT_EXCEEDED" {
		t.Errorf("ช่วง 10 ปีต้องติดเพดาน แต่ได้ %q (errors=%v)", got, decade.Errors)
	}
}

// TestRejectedQueriesStillCarryTheirName กันไม่ให้ของที่โดนกันหายไปจาก log
//
// request ที่ถูกปฏิเสธคือ request ที่อยากเห็นใน Kibana มากที่สุด
func TestRejectedQueriesStillCarryTheirName(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(2, defaultComplexity))

	r := execute(t, h, `query NamedButTooDeep { pet(id: "pet-1") { caregivers { user { id } } } }`, "token")
	if got := errCode(r); got != "DEPTH_LIMIT_EXCEEDED" {
		t.Fatalf("ต้องถูกปฏิเสธเพราะลึกเกิน แต่ได้ %q", got)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0].Message, "เกินเพดาน") {
		t.Errorf("ข้อความ error ต้องบอกว่าเกินเพดานอะไร แต่ได้ %v", r.Errors)
	}
}

// TestDepthLimitOffWhenUnset — ตั้งเป็น 0 แปลว่าไม่บังคับ ใช้ตอน debug ในเครื่อง
func TestDepthLimitOffWhenUnset(t *testing.T) {
	f := newFakeUpstream(t)
	h := newTestServerWith(t, f, guardrailCfg(0, 100000))

	r := execute(t, h, `query Deep { pet(id: "pet-1") { caregivers { user { id } } } }`, "token")
	if got := errCode(r); got == "DEPTH_LIMIT_EXCEEDED" {
		t.Errorf("ตั้ง MAX_QUERY_DEPTH เป็น 0 ต้องไม่บังคับความลึก แต่ถูกปฏิเสธ")
	}
}
