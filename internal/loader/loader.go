// Package loader กัน N+1 ด้วยการดึงครั้งเดียวต่อหนึ่ง request แล้วใช้ซ้ำ
//
// ทำไมเป็น cache ไม่ใช่ batcher แบบ dataloader ทั่วไป
//
// dataloader มาตรฐานจะรวบ key ที่ขอเข้ามาในช่วงเวลาสั้นๆ แล้วยิงทีเดียว
// ซึ่งต้องมี endpoint ที่รับหลาย id พร้อมกัน — แต่ auth-service ไม่มี
// endpoint แบบนั้นเลย ตัวที่ list user ได้คือ /admin/users ซึ่งคืนมาทั้งหมดอยู่แล้ว
//
// เมื่อ upstream คืนทั้งชุดในครั้งเดียว การ cache ต่อ request จึงได้ผลเท่ากัน
// (ยิง 1 ครั้งไม่ว่าจะมีผู้ดูแลกี่คน) แต่โค้ดน้อยกว่าและไม่ต้องจัดการ goroutine
//
// ถ้าวันหนึ่งเพิ่ม endpoint แบบ resolve หลาย id ค่อยเปลี่ยนข้างในไฟล์นี้
// โดยที่ resolver ไม่ต้องแก้
package loader

import (
	"context"
	"sync"

	"github.com/vertex/bff/internal/client"
)

type ctxKey struct{}

type Loaders struct {
	auth *client.AuthClient
	pet  *client.PetClient

	usersOnce sync.Once
	users     map[string]client.User
	usersErr  error

	petsOnce sync.Once
	pets     map[string]client.Pet
	petsErr  error

	meOnce sync.Once
	me     *client.Me
	meErr  error

	srcMu   sync.RWMutex
	srcPets map[string]client.Pet
}

// Me คือผู้ใช้ที่ถือ token ของ request นี้
//
// ดึงจาก /auth/me แทนการแกะ JWT เอง เพราะ BFF ไม่ควรตีความ token
// การตรวจ token เป็นหน้าที่ของ service ปลายทาง ที่นี่แค่ถามว่า "ฉันคือใคร"
func (l *Loaders) Me(ctx context.Context) (*client.Me, error) {
	l.meOnce.Do(func() {
		l.me, l.meErr = l.auth.Me(ctx)
	})
	return l.me, l.meErr
}

func New(auth *client.AuthClient, pet *client.PetClient) *Loaders {
	return &Loaders{auth: auth, pet: pet}
}

func With(ctx context.Context, l *Loaders) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// From คืน loader ของ request ปัจจุบัน
//
// คืน nil ได้ถ้าถูกเรียกนอก request — resolver ต้องเช็คก่อนใช้เสมอ
func From(ctx context.Context) *Loaders {
	l, _ := ctx.Value(ctxKey{}).(*Loaders)
	return l
}

// User แปลง user id เป็นข้อมูลผู้ใช้
//
// คืน user ที่มีแต่ id ถ้าหาไม่เจอหรือไม่มีสิทธิ์ดู แทนการคืน error
// เพราะการที่แสดงชื่อผู้ดูแลไม่ได้ ไม่ควรทำให้ทั้งหน้าจอพัง
func (l *Loaders) User(ctx context.Context, id string) client.User {
	l.usersOnce.Do(func() {
		users, err := l.auth.AdminListUsers(ctx, "")
		if err != nil {
			// เก็บ error ไว้ดูได้ แต่ไม่ทำให้ resolver ล้ม
			l.usersErr = err
			l.users = map[string]client.User{}
			return
		}
		m := make(map[string]client.User, len(users))
		for _, u := range users {
			m[u.ID] = u
		}
		l.users = m
	})

	if u, ok := l.users[id]; ok {
		return u
	}
	return client.User{ID: id}
}

// UsersErr บอกว่าการโหลดรายชื่อผู้ใช้ล้มเหลวหรือไม่
// ใช้เขียน log ได้ว่าทำไมชื่อถึงว่าง โดยไม่ทำให้ query พัง
func (l *Loaders) UsersErr() error { return l.usersErr }

// Pet แปลง pet id เป็นข้อมูลสัตว์เลี้ยง ใช้กับ Event.pet
//
// ใช้เส้น admin เพราะ event log เปิดให้เฉพาะ SUPER_ADMIN อยู่แล้ว
// ผู้เรียกที่มาถึงตรงนี้จึงมีสิทธิ์เห็นสัตว์เลี้ยงทุกตัวแน่นอน
func (l *Loaders) Pet(ctx context.Context, id string) (client.Pet, bool) {
	l.petsOnce.Do(func() {
		pets, err := l.pet.AdminListPets(ctx)
		if err != nil {
			l.petsErr = err
			l.pets = map[string]client.Pet{}
			return
		}
		m := make(map[string]client.Pet, len(pets))
		for _, p := range pets {
			m[p.ID] = p
		}
		l.pets = m
	})

	p, ok := l.pets[id]
	return p, ok
}

func (l *Loaders) PetsErr() error { return l.petsErr }

// -----------------------------------------------------------------------------
// จำ DTO ต้นทางของสัตว์เลี้ยงไว้ใช้ตอน resolve field ลูก
// -----------------------------------------------------------------------------
//
// model ของ GraphQL ไม่มี ownerId กับ caregivers อยู่ในตัว (โดยตั้งใจ —
// สองอย่างนั้นเป็น field ที่ต้อง resolve แยก) แต่ resolver ของ owner,
// caregivers และ viewerPermissions ต้องใช้ข้อมูลจาก DTO ต้นทาง
//
// การจำไว้ตอนแปลงครั้งแรกทำให้ไม่ต้องยิงถามสัตว์เลี้ยงตัวเดิมซ้ำ
// ซึ่งจะเป็น N+1 รอบใหม่ที่ย้ายมาอยู่ฝั่ง server แทน

func (l *Loaders) RememberPet(p client.Pet) {
	l.srcMu.Lock()
	defer l.srcMu.Unlock()
	if l.srcPets == nil {
		l.srcPets = map[string]client.Pet{}
	}
	l.srcPets[p.ID] = p
}

// SourcePet คืน DTO ที่จำไว้ ถ้ายังไม่มีจะไปดึงมาแล้วจำไว้ให้
func (l *Loaders) SourcePet(ctx context.Context, id string) (client.Pet, error) {
	l.srcMu.RLock()
	p, ok := l.srcPets[id]
	l.srcMu.RUnlock()
	if ok {
		return p, nil
	}

	fetched, err := l.pet.GetPet(ctx, id)
	if err != nil {
		return client.Pet{}, err
	}
	l.RememberPet(*fetched)
	return *fetched, nil
}
