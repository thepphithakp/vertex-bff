package graph

// ไฟล์นี้ gqlgen ไม่ generate ทับ — ใช้ผูก dependency เข้า resolver

import (
	"github.com/vertex/bff/internal/client"
	"github.com/vertex/bff/internal/config"
)

// Resolver ถือ client ของทุก service
//
// ชื่อ field ลงท้ายด้วย Svc เพราะ resolver ที่ฝังโครงสร้างนี้มี method
// ชื่อ Pets / Events ตาม field ของ schema อยู่แล้ว ถ้าตั้งชื่อ field ซ้ำ
// method จะบัง field จนเรียกไม่ถึง
type Resolver struct {
	PetSvc   *client.PetClient
	AuthSvc  *client.AuthClient
	EventSvc *client.EventClient
	Cfg      config.Config
}
