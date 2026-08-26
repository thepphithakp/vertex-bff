package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port string

	PetServiceURL   string
	AuthServiceURL  string
	EventServiceURL string

	// PublicBaseURL คือ URL ที่ client มองเห็นจากภายนอก ใช้ประกอบ avatarUrl
	//
	// ต้องแยกจาก PetServiceURL เพราะอันนั้นเป็นที่อยู่ภายในคลัสเตอร์
	// ที่ client เรียกไม่ถึง ถ้าเผลอเอามาใช้ รูปจะโหลดไม่ขึ้นทั้งแอป
	PublicBaseURL string

	UpstreamTimeout time.Duration

	// MaxQueryDepth / MaxQueryComplexity กันคนยิง query ซ้อนลึกจนล้ม backend
	// ดู VT-99 — endpoint นี้เปิดออกอินเทอร์เน็ต rate limit แบบนับ request
	// ใช้ไม่ได้กับ GraphQL เพราะหนึ่ง request แพงเท่าไหร่ก็ได้
	//
	// สองตัวนี้กันคนละอย่าง ต้องมีทั้งคู่:
	// depth กัน query ที่ซ้อนวนเป็นทอดๆ ส่วน complexity กัน query ที่ขอ list ใหญ่
	// query ที่ลึกอาจราคาถูกมาก และ query ที่แพงมากอาจลึกแค่สามชั้น
	//
	// MaxQueryDepth ตั้งไว้เท่ากับความลึกสูงสุดที่ schema ปัจจุบันเป็นไปได้ (9)
	//
	// ตอนนี้ schema ไม่มี cycle — `User` ไม่มี field ที่วนกลับไปหา `Pet` —
	// ความลึกจึงถูกจำกัดด้วยตัว schema เองอยู่แล้ว เพดานนี้ยังไม่เคยกันอะไรจริง
	//
	// ที่ยังต้องมีเพราะมันเป็นสัญญาณเตือนตอน schema เปลี่ยน ถ้าวันหนึ่งมีคนเพิ่ม
	// `User.pets` เข้าไป จะเกิด cycle ทันทีและ query ซ้อนไม่รู้จบจะเป็นไปได้
	// ค่านี้ทำให้มันถูกปฏิเสธแทนที่จะทำให้ resolver ระเบิด
	// (`TestSchemaDepthMatchesLimit` บังคับให้สองค่านี้ตรงกันเสมอ)
	//
	// ตั้งเป็น 0 = ไม่บังคับ ใช้ตอน debug ในเครื่องเท่านั้น
	MaxQueryDepth      int
	MaxQueryComplexity int

	// EnableIntrospection ปิดไว้บน production
	EnableIntrospection bool
}

func Load() (Config, error) {
	c := Config{
		Port:                env("PORT", "3000"),
		PetServiceURL:       env("PET_SERVICE_URL", "http://localhost:4001"),
		AuthServiceURL:      env("AUTH_SERVICE_URL", "http://localhost:4000"),
		EventServiceURL:     env("EVENT_SERVICE_URL", "http://localhost:4002"),
		PublicBaseURL:       env("PUBLIC_BASE_URL", ""),
		UpstreamTimeout:     envDuration("UPSTREAM_TIMEOUT", 10*time.Second),
		MaxQueryDepth:       envInt("MAX_QUERY_DEPTH", 9),
		MaxQueryComplexity:  envInt("MAX_QUERY_COMPLEXITY", 5000),
		EnableIntrospection: envBool("ENABLE_INTROSPECTION", false),
	}

	if c.PublicBaseURL == "" {
		return c, fmt.Errorf("ต้องตั้ง PUBLIC_BASE_URL ไม่งั้น avatarUrl จะชี้ไปที่อยู่ภายในคลัสเตอร์ที่ client เรียกไม่ถึง")
	}
	return c, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
