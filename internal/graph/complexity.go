package graph

import (
	"time"
)

// ต้นทุนของ field ที่ต้องยิงออกไปหา service จริง
//
// ค่าเริ่มต้นของ gqlgen คือทุก field ราคา 1 เท่ากันหมด ซึ่งอ่อนเกินไป —
// `id` ที่อ่านจาก struct ในหน่วยความจำกับ `pet(id:)` ที่ต้องยิง HTTP ข้าม
// service ไม่ควรราคาเท่ากัน
//
// ตัวเลขพวกนี้ไม่ได้อิงหน่วยอะไรจริง เป็นแค่การบอกว่า "แพงกว่าอ่าน field เฉยๆ
// ประมาณสิบเท่า" เทียบกับเพดาน MAX_QUERY_COMPLEXITY ที่ตั้งไว้ 1000
const (
	costUpstreamCall = 10  // หนึ่ง request ออกไปหา service
	costLLMCall      = 250 // หนึ่ง request ออกไปหา LLM นอกคลัสเตอร์ + กิน quota
	costLoaderCall   = 5   // ผ่าน loader ที่รวบให้เหลือครั้งเดียวต่อ request
	costNamespace    = 1   // field ที่ไม่ทำอะไรเอง ลูกเป็นคนทำงาน
)

// จำนวนแถวที่เดาไว้สำหรับ list ที่ไม่มี `first` ให้จำกัด
//
// ต้องเดา เพราะรู้จำนวนจริงตอน resolve ซึ่งสายเกินไปแล้ว การประเมินสูงไว้ก่อน
// ปลอดภัยกว่า — ผลเสียคือ query ที่ซ้อนลึกมากๆ อาจโดนปฏิเสธทั้งที่ข้อมูลจริงน้อย
// ซึ่งแก้ได้ด้วยการแบ่ง query ส่วนผลเสียของการประเมินต่ำไปคือ server ล้ม
const (
	assumedPetsPerUser       = 20
	assumedCaregiversPerPet  = 10
	assumedPermissionsPerRow = 10
)

// ceiling กันตัวเลขล้น ไม่ใช่เพดานเชิงนโยบาย
//
// ห้าม cap จำนวนวันให้ต่ำกว่าความจริง เพราะ resolver สร้างถังตามจำนวนวันจริงๆ
// ขอ 10 ปีย้อนหลังได้ 3650 ถัง ถ้าคิดราคาแค่ 400 วันจะกลายเป็นว่า query แพงมาก
// ดูถูกกว่าความเป็นจริงเกือบสิบเท่า แล้วเพดาน complexity ก็กันไม่ได้
const maxSummaryDaysCeiling = 100000

// NewComplexityRoot กำหนดราคาของ field ที่แพงกว่าปกติ
//
// field ที่ไม่ได้ตั้งไว้ที่นี่ใช้ค่าเริ่มต้นของ gqlgen คือ childComplexity + 1
//
// หลักการมีสองข้อ
//
//  1. field ที่ยิงออกไปหา service มีราคาคงที่บวกเพิ่ม แม้จะไม่ได้ขอลูกอะไรเลย
//  2. field ที่เป็น list ต้อง **คูณ** ราคาของลูกด้วยจำนวนแถว ไม่ใช่บวก 1
//     เพราะ resolver จะทำงานซ้ำเท่าจำนวนแถวจริงๆ
//
// ข้อ 2 คือหัวใจ — ถ้าไม่มี query แบบนี้ราคาแค่ประมาณ 5 ทั้งที่ทำให้
// resolver ทำงานเป็นพันครั้ง
//
//	query Evil {
//	  admin { pets(first: 200) { edges { node { caregivers { user { id } } } } } }
//	}
func NewComplexityRoot() ComplexityRoot {
	var c ComplexityRoot

	// ---------------------------------------------------------------------
	// จุดเริ่มของ query
	// ---------------------------------------------------------------------
	c.Query.Viewer = func(childComplexity int) int {
		return childComplexity + costUpstreamCall
	}
	c.Query.Pet = func(childComplexity int, id string) int {
		return childComplexity + costUpstreamCall
	}
	c.Query.Admin = func(childComplexity int) int {
		return childComplexity + costNamespace
	}

	// ---------------------------------------------------------------------
	// list ที่ผู้เรียกกำหนดขนาดได้
	// ---------------------------------------------------------------------
	// ใช้ pageSize() ตัวเดียวกับที่ resolver ใช้ ราคาจะได้ตรงกับจำนวนแถว
	// ที่จะไปดึงมาจริงๆ ไม่ใช่ตัวเลขที่ client ขอมาลอยๆ
	c.AdminQuery.Users = func(childComplexity int, first *int, after *string, search *string) int {
		return childComplexity*pageSize(first) + costUpstreamCall
	}
	c.AdminQuery.Pets = func(childComplexity int, first *int, after *string) int {
		return childComplexity*pageSize(first) + costUpstreamCall
	}
	c.AdminQuery.Events = func(childComplexity int, first *int, after *string, entityType *string, entityID *string, actorID *string) int {
		return childComplexity*pageSize(first) + costUpstreamCall
	}
	c.Pet.LitterLogs = func(childComplexity int, first *int, after *string, from *time.Time, to *time.Time) int {
		return childComplexity*pageSize(first) + costUpstreamCall
	}
	c.Pet.WaterLogs = func(childComplexity int, first *int, after *string, from *time.Time, to *time.Time) int {
		return childComplexity*pageSize(first) + costUpstreamCall
	}

	// ---------------------------------------------------------------------
	// list ที่ผู้เรียกกำหนดขนาดไม่ได้
	// ---------------------------------------------------------------------
	c.Viewer.Pets = func(childComplexity int) int {
		return childComplexity*assumedPetsPerUser + costUpstreamCall
	}
	c.Pet.Caregivers = func(childComplexity int) int {
		return childComplexity * assumedCaregiversPerPet
	}
	c.PetCaregiver.Permissions = func(childComplexity int) int {
		return childComplexity * assumedPermissionsPerRow
	}

	// ---------------------------------------------------------------------
	// สรุปรายวัน — ราคาขึ้นกับจำนวนวันที่ขอ
	// ---------------------------------------------------------------------
	// ตัว summary ต้องดึง log ทั้งช่วงมา group และจำนวนถังใน `daily`
	// เท่ากับจำนวนวัน ซึ่ง `daily` เองมองไม่เห็น from/to จึงต้องคิดที่นี่
	c.Pet.LitterSummary = func(childComplexity int, from time.Time, to time.Time) int {
		return childComplexity*summaryDays(from, to) + costUpstreamCall
	}
	c.Pet.WaterSummary = func(childComplexity int, from time.Time, to time.Time) int {
		return childComplexity*summaryDays(from, to) + costUpstreamCall
	}

	// คำวิเคราะห์ด้วย LLM — แพงกว่าฟิลด์อื่นทุกตัว
	//
	// ต้องดึง log ทั้งช่วงเหมือน summary แล้วยังออกไปนอกคลัสเตอร์ต่อ
	// ซึ่งกิน quota ที่มีเพดานต่อนาที ไม่ใช่แค่กิน CPU ของเราเอง
	// ตั้งไว้แพงเพื่อให้ query ที่ขอฟิลด์นี้ให้แมวหลายตัวพร้อมกันชนเพดาน
	// ตั้งแต่ต้นแทนที่จะไปเผา quota จนหมดแล้วค่อยรู้
	c.Pet.WaterInsight = func(childComplexity int, from time.Time, to time.Time) int {
		return childComplexity*summaryDays(from, to) + costLLMCall
	}

	// ---------------------------------------------------------------------
	// field ที่ต้องไปตาม service อื่นต่อ
	// ---------------------------------------------------------------------
	// ผ่าน loader ที่ดึงครั้งเดียวต่อ request จึงถูกกว่าการยิงตรง แต่ไม่ฟรี
	// เพราะยังต้องประกอบข้อมูลต่อแถว
	c.PetCaregiver.User = func(childComplexity int) int {
		return childComplexity + costLoaderCall
	}
	c.Event.Pet = func(childComplexity int) int {
		return childComplexity + costLoaderCall
	}

	return c
}

// summaryDays นับจำนวนวันในช่วง อย่างน้อย 1 และไม่เกินเพดาน
func summaryDays(from, to time.Time) int {
	if to.Before(from) {
		return 1
	}
	days := int(to.Sub(from).Hours()/24) + 1
	if days < 1 {
		return 1
	}
	if days > maxSummaryDaysCeiling {
		return maxSummaryDaysCeiling
	}
	return days
}
