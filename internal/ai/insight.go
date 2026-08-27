package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// WaterFacts คือทุกอย่างที่ถูกส่งออกไปนอกคลัสเตอร์
//
// ตั้งใจให้เป็น struct แยกแทนการโยน model.Pet ทั้งก้อนเข้าไป เพราะอยากให้
// เห็นได้จากที่เดียวว่ามีอะไรไปถึง Google บ้าง — **ห้ามใส่ user id, email
// หรือ pet id ลงในนี้** ชื่อเล่นของสัตว์เลี้ยงใส่ได้เพื่อให้สำนวนเป็นธรรมชาติ
type WaterFacts struct {
	PetName     string
	Species     string
	AgeLabel    string
	WeightKg    *float64
	TodayMl     int
	TargetMl    *int
	AvgMlPerDay float64
	DaysInRange int
	// จำนวนวันในช่วงที่กินน้ำต่ำกว่าเป้า ใช้บอกว่าเป็นวันแย่วันเดียว
	// หรือเป็นแนวโน้มติดกันหลายวัน ซึ่งเปลี่ยนคำแนะนำคนละแบบ
	DaysBelowTarget int
}

type Insight struct {
	Text        string
	Model       string
	GeneratedAt time.Time
	Cached      bool
}

// Service ห่อ Gemini ด้วย cache
//
// free tier มีเพดานต่อนาทีและต่อวัน ถ้ายิงใหม่ทุกครั้งที่เปิดหน้า
// แค่ผู้ใช้คนเดียวสลับหน้าไปมาก็หมดโควตาได้ — cache key คิดจากตัวเลขจริง
// ไม่ใช่เวลา ข้อความจึงเปลี่ยนเมื่อพฤติกรรมเปลี่ยน ไม่ใช่เปลี่ยนเพราะครบชั่วโมง
type Service struct {
	gemini *Gemini
	ttl    time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
	now   func() time.Time
}

type cacheEntry struct {
	insight Insight
	expires time.Time
}

func NewService(g *Gemini, ttl time.Duration) *Service {
	return &Service{
		gemini: g,
		ttl:    ttl,
		cache:  make(map[string]cacheEntry),
		now:    time.Now,
	}
}

func (s *Service) Enabled() bool { return s != nil && s.gemini.Enabled() }

const systemPrompt = `คุณเป็นผู้ช่วยของแอปดูแลสัตว์เลี้ยง เขียนสรุปสั้นๆ ให้เจ้าของอ่านเข้าใจทันที

กติกาที่ห้ามละเมิด
- ตอบเป็นภาษาไทย ยาว 1-3 ประโยค ไม่ต้องมีหัวข้อ ไม่ต้องใช้ bullet
- ห้ามวินิจฉัยโรคและห้ามสั่งยา บอกได้แค่ว่าอาการแบบไหนควรพาไปหาสัตวแพทย์
- อ้างอิงเฉพาะตัวเลขที่ได้รับ ห้ามแต่งตัวเลขหรือข้อมูลที่ไม่ได้ให้มา
- ถ้าข้อมูลไม่พอให้บอกตรงๆ ว่ายังสรุปไม่ได้ ห้ามเดา
- น้ำเสียงเป็นกันเอง ไม่ตื่นตระหนก และห้ามขึ้นต้นด้วยคำทักทาย

เรื่องสำนวน — เจ้าของเปิดอ่านทุกวัน ข้อความที่ขึ้นต้นเหมือนเดิมทุกครั้งจะถูกมองข้าม
- **ห้ามไล่ทวนตัวเลขทุกตัวที่ได้รับ** หยิบมาเฉพาะตัวที่ทำให้เข้าใจสถานการณ์
- เปลี่ยนรูปประโยคเปิดทุกครั้ง อย่าเริ่มด้วยโครงเดิมซ้ำๆ
- ไม่ต้องปิดท้ายด้วยคำเตือนเรื่องสัตวแพทย์ทุกครั้ง ใส่เมื่อตัวเลขบอกว่าควรใส่จริงๆ`

// angles ทำให้ข้อความไม่ซ้ำรูปเดิมทุกวัน
//
// เลือกจากลายนิ้วมือของตัวเลข ไม่ได้สุ่ม — ข้อมูลชุดเดิมจึงได้มุมเดิมเสมอ
// ซึ่งจำเป็นเพราะ cache เก็บผลลัพธ์ไว้ ถ้าสุ่มจะได้คนละข้อความทุกครั้งที่
// cache หมดอายุทั้งที่ข้อมูลไม่ได้เปลี่ยน แล้วเจ้าของจะสับสนว่าเกิดอะไรขึ้น
var angles = []string{
	"คราวนี้เน้นว่าควรทำอะไรต่อในวันนี้",
	"คราวนี้เน้นแนวโน้มของทั้งสัปดาห์ว่าดีขึ้นหรือแย่ลง",
	"คราวนี้เน้นเทียบกับเป้าหมายของวันนี้เป็นหลัก",
	"คราวนี้เน้นข้อสังเกตที่เจ้าของน่าจะมองข้าม",
	"คราวนี้เน้นให้กำลังใจถ้าทำได้ดี หรือเตือนอย่างอ่อนโยนถ้ายังห่างเป้า",
}

func angleFor(fingerprint string) string {
	if fingerprint == "" {
		return angles[0]
	}
	// ใช้ไบต์แรกของ fingerprint พอ ไม่ต้องกระจายให้สวยงามมาก
	return angles[int(fingerprint[0])%len(angles)]
}

func (s *Service) WaterInsight(ctx context.Context, f WaterFacts) (*Insight, error) {
	if !s.Enabled() {
		return nil, ErrDisabled
	}

	key := f.fingerprint()
	if hit, ok := s.get(key); ok {
		return &hit, nil
	}

	// model ที่ตอบอาจไม่ใช่ตัวแรกในลำดับ ถ้าตัวหลักหมดโควตาไปแล้ว
	text, model, err := s.gemini.Generate(ctx, systemPrompt, f.userPrompt(angleFor(key)))
	if err != nil {
		return nil, err
	}

	ins := Insight{
		Text:        text,
		Model:       model,
		GeneratedAt: s.now(),
	}
	s.put(key, ins)
	return &ins, nil
}

func (s *Service) get(key string) (Insight, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || s.now().After(e.expires) {
		return Insight{}, false
	}
	hit := e.insight
	hit.Cached = true
	return hit, true
}

func (s *Service) put(key string, ins Insight) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// เก็บกวาดของหมดอายุตอนเขียน — map นี้โตตามจำนวนสัตว์เลี้ยงที่มีคนเปิดดู
	// ไม่ได้โตตามจำนวน request จึงไม่ต้องใช้ LRU เต็มรูปแบบ
	now := s.now()
	for k, v := range s.cache {
		if now.After(v.expires) {
			delete(s.cache, k)
		}
	}
	s.cache[key] = cacheEntry{insight: ins, expires: now.Add(s.ttl)}
}

// fingerprint ทำให้ข้อความถูก generate ใหม่เมื่อ "ตัวเลขที่เล่าเรื่อง" เปลี่ยน
//
// ปริมาณน้ำปัดเป็นช่วงละ 10 ml เพราะการบันทึกเพิ่มทีละ 5 ml ไม่ได้เปลี่ยน
// เนื้อหาคำแนะนำ แต่จะทำให้ยิง Gemini ใหม่ทุกครั้งที่กดเพิ่มถ้าไม่ปัด
func (f WaterFacts) fingerprint() string {
	weight := "?"
	if f.WeightKg != nil {
		weight = fmt.Sprintf("%.1f", *f.WeightKg)
	}
	target := "?"
	if f.TargetMl != nil {
		target = fmt.Sprint(*f.TargetMl)
	}
	raw := strings.Join([]string{
		f.PetName, f.Species, f.AgeLabel, weight, target,
		fmt.Sprint(f.TodayMl / 10),
		fmt.Sprintf("%.0f", f.AvgMlPerDay/10),
		fmt.Sprint(f.DaysInRange),
		fmt.Sprint(f.DaysBelowTarget),
	}, "|")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:16])
}

func (f WaterFacts) userPrompt(angle string) string {
	var b strings.Builder
	b.WriteString("ข้อมูลการกินน้ำของสัตว์เลี้ยง\n")
	fmt.Fprintf(&b, "- ชื่อ: %s\n", f.PetName)
	fmt.Fprintf(&b, "- ชนิด: %s\n", f.Species)
	if f.AgeLabel != "" {
		fmt.Fprintf(&b, "- อายุ: %s\n", f.AgeLabel)
	}
	if f.WeightKg != nil {
		fmt.Fprintf(&b, "- น้ำหนัก: %.1f กก.\n", *f.WeightKg)
	} else {
		b.WriteString("- น้ำหนัก: ยังไม่ได้บันทึก\n")
	}
	fmt.Fprintf(&b, "- วันนี้ดื่มไปแล้ว: %d ml\n", f.TodayMl)
	if f.TargetMl != nil {
		fmt.Fprintf(&b, "- เป้าหมายต่อวัน: %d ml (คิดจากน้ำหนักตัว)\n", *f.TargetMl)
	} else {
		// บอก model ตรงๆ ว่าไม่รู้ ดีกว่าปล่อยให้เดาเกณฑ์เอง
		// ซึ่งเป็นบั๊กเดิมของฝั่งแอปที่เดาน้ำหนักเป็น 4 กก.
		b.WriteString("- เป้าหมายต่อวัน: ยังคำนวณไม่ได้เพราะไม่มีน้ำหนัก\n")
	}
	fmt.Fprintf(&b, "- ค่าเฉลี่ย %d วันล่าสุด: %.0f ml ต่อวัน\n", f.DaysInRange, f.AvgMlPerDay)
	if f.TargetMl != nil {
		fmt.Fprintf(&b, "- ในช่วงนั้นมี %d วันที่ได้น้ำต่ำกว่าเป้าหมาย\n", f.DaysBelowTarget)
	}
	b.WriteString("\nสรุปให้เจ้าของฟังว่าตอนนี้เป็นยังไง")
	if angle != "" {
		b.WriteString(" — " + angle)
	}
	return b.String()
}
