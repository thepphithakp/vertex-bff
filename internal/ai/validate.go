package ai

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// errBadOutput คือข้อความที่ไม่ควรปล่อยให้ถึงตาผู้ใช้
//
// LLM ที่เขียนเพี้ยนไม่ได้แจ้ง error อะไรกลับมา มันตอบ 200 ตามปกติ
// ด่านนี้จึงเป็นที่เดียวที่จับได้ก่อนข้อความจะไปโผล่บนหน้าจอ
var errBadOutput = errors.New("ai: ข้อความที่ได้ไม่ผ่านการตรวจ")

// maxInsightRunes กันข้อความยาวเกินการ์ดบนมือถือ
//
// 3 ประโยคภาษาไทยยาวสุดราว 300 ตัวอักษร เกินกว่านี้แปลว่าโมเดลไม่ทำตามที่สั่ง
const maxInsightRunes = 600

// validateInsight ตรวจข้อความก่อนเก็บลง cache และส่งให้ client
//
// ตรวจเท่าที่ตรวจได้จริงและไม่ตัดสินผิดง่าย — ไม่ได้พยายามอ่านภาษาไทยให้เข้าใจ
// แค่จับรูปแบบความเสียหายที่เคยเห็นกับตาตอนทดสอบ
func validateInsight(text string, f WaterFacts) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("%w: ว่างเปล่า", errBadOutput)
	}
	if utf8.RuneCountInString(text) > maxInsightRunes {
		return fmt.Errorf("%w: ยาวเกินไป (%d ตัวอักษร)", errBadOutput, utf8.RuneCountInString(text))
	}
	if err := checkPetName(text, f.PetName); err != nil {
		return err
	}
	return nil
}

// checkPetName จับกรณีที่ชื่อสัตว์เลี้ยงถูกเขียนขาดตัว
//
// เจอจริงตอนทดสอบ: ชื่อ "ส้มโอ" ออกมาเป็น "วันนี้ส้ม เพิ่งดื่มน้ำ..."
// ซึ่งเจ้าของอ่านแล้วสะดุดทันทีเพราะเป็นชื่อที่เขาตั้งเอง
//
// ไม่ใช้ชื่อไม่ผิดกติกา — โมเดลจะเรียก "น้องแมว" ก็ได้ ด่านนี้จึงตรวจเฉพาะ
// กรณีที่ "ชื่อเต็มไม่มี แต่ส่วนต้นของชื่อโผล่มา" ซึ่งแปลว่าเขียนค้างไว้
func checkPetName(text, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(text, name) {
		return nil
	}

	runes := []rune(name)
	if len(runes) < 3 {
		// ชื่อสั้นเกินกว่าจะแยกออกว่าขาดตัวหรือบังเอิญไปตรงกับคำอื่น
		return nil
	}
	for n := len(runes) - 1; n >= 2; n-- {
		if strings.Contains(text, string(runes[:n])) {
			return fmt.Errorf("%w: ชื่อ %q ถูกเขียนขาดเหลือ %q",
				errBadOutput, name, string(runes[:n]))
		}
	}
	return nil
}
