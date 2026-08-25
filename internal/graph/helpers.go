package graph

import (
	"context"
	"errors"
	"time"

	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vertex/bff/internal/client"
	"github.com/vertex/bff/internal/graph/model"
	"github.com/vertex/bff/internal/loader"
)

// -----------------------------------------------------------------------------
// error
// -----------------------------------------------------------------------------
//
// ข้อผิดพลาดที่คาดไว้ออกไปเป็น GraphQL error พร้อม extensions.code
// เพื่อให้ client แยกกรณีได้โดยไม่ต้องจับคู่ข้อความภาษาไทย ซึ่งเปลี่ยนได้ตลอด

func gqlErr(err error) error {
	var ce *client.Error
	if errors.As(err, &ce) {
		ext := map[string]any{"code": ce.Code}
		if ce.RequestID != "" {
			// ส่ง requestId ต่อให้ client เพื่อให้ตามหาใน Kibana ได้
			ext["requestId"] = ce.RequestID
		}
		return &gqlerror.Error{Message: ce.Message, Extensions: ext}
	}
	return &gqlerror.Error{
		Message:    err.Error(),
		Extensions: map[string]any{"code": "INTERNAL"},
	}
}

func badRequest(msg string) error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "VALIDATION"}}
}

func internalErr(msg string) error {
	return &gqlerror.Error{Message: msg, Extensions: map[string]any{"code": "INTERNAL"}}
}

func asErr(err error, target **client.Error) bool {
	return errors.As(err, target)
}

// -----------------------------------------------------------------------------
// pet
// -----------------------------------------------------------------------------

// petModel แปลง DTO เป็น model พร้อมจำต้นทางไว้ให้ field ลูกใช้
//
// ถ้าไม่จำไว้ resolver ของ owner / caregivers / viewerPermissions
// จะต้องยิงถามสัตว์เลี้ยงตัวเดิมซ้ำอีกครั้งละหนึ่ง request
func (r *Resolver) petModel(ctx context.Context, p client.Pet) *model.Pet {
	if l := loader.From(ctx); l != nil {
		l.RememberPet(p)
	}
	return toPet(p)
}

func (r *Resolver) source(ctx context.Context, petID string) (client.Pet, error) {
	l := loader.From(ctx)
	if l == nil {
		p, err := r.PetSvc.GetPet(ctx, petID)
		if err != nil {
			return client.Pet{}, err
		}
		return *p, nil
	}
	return l.SourcePet(ctx, petID)
}

// caregiverWithUser ใส่ id ของผู้ใช้ไว้ก่อน ให้ resolver ของ user ไปเติมชื่อทีหลัง
//
// ทำแบบนี้เพื่อให้ query ที่ขอแค่ permissions ไม่ต้องไปแตะ auth-service เลย
func caregiverWithUser(c client.Caregiver) *model.PetCaregiver {
	m := toCaregiver(c)
	m.User = &model.User{ID: c.UserID}
	return m
}

// -----------------------------------------------------------------------------
// input
// -----------------------------------------------------------------------------

func petInputBody(in model.PetInput) map[string]any {
	return map[string]any{
		"name": in.Name, "species": in.Species, "breed": in.Breed,
		"colorCode": in.ColorCode, "birthDate": in.BirthDate, "gender": in.Gender,
		"currentWeight": in.CurrentWeight, "microchipId": in.MicrochipID,
		"isSpayedNeutered": in.IsSpayedNeutered, "bloodType": in.BloodType,
		"allergies": in.Allergies, "personality": in.Personality,
	}
}

// litterInputBody คง id ที่ client ส่งมาไว้
//
// REST ยอมให้ client กำหนด id เองเพื่อให้บันทึกตอนออฟไลน์แล้วส่งซ้ำได้
// โดยไม่เกิดรายการซ้ำ ถ้า BFF ตัดทิ้งจะทำให้พฤติกรรมนี้พังทันที
func litterInputBody(in model.LitterLogInput) map[string]any {
	b := map[string]any{"type": in.Type, "amount": in.Amount}
	if in.ID != nil {
		b["id"] = *in.ID
	}
	if in.Date != nil {
		b["date"] = *in.Date
	}
	return b
}

func waterInputBody(in model.WaterLogInput) map[string]any {
	b := map[string]any{"amount": in.Amount}
	if in.ID != nil {
		b["id"] = *in.ID
	}
	if in.Date != nil {
		b["date"] = *in.Date
	}
	return b
}

// -----------------------------------------------------------------------------
// log
// -----------------------------------------------------------------------------

func inRange(t time.Time, from, to *time.Time) bool {
	if from != nil && t.Before(*from) {
		return false
	}
	if to != nil && t.After(*to) {
		return false
	}
	return true
}

func filterLitterByRange(logs []client.LitterLog, from, to *time.Time) []client.LitterLog {
	if from == nil && to == nil {
		return logs
	}
	out := logs[:0:0]
	for _, l := range logs {
		if inRange(l.Date, from, to) {
			out = append(out, l)
		}
	}
	return out
}

func filterWaterByRange(logs []client.WaterLog, from, to *time.Time) []client.WaterLog {
	if from == nil && to == nil {
		return logs
	}
	out := logs[:0:0]
	for _, l := range logs {
		if inRange(l.Date, from, to) {
			out = append(out, l)
		}
	}
	return out
}

// summaryMaxPages จำกัดจำนวนหน้าที่ไล่ดึงเพื่อทำสรุป
//
// REST ยังไม่มี filter ตามวันที่ จึงต้องไล่ย้อนจากใหม่ไปเก่าจนพ้นช่วงที่ขอ
// เพดานนี้กันไม่ให้ query เดียวไล่ทั้งประวัติจนกลายเป็นภาระของ backend
// เมื่อ pet-service รองรับ from/to แล้วให้เอาลูปนี้ออกทั้งหมด
const (
	summaryMaxPages = 20
	summaryPageSize = 200
)

func (r *Resolver) allLitterInRange(ctx context.Context, petID string, from, to time.Time) ([]client.LitterLog, error) {
	var all []client.LitterLog
	var cursor *string
	for i := 0; i < summaryMaxPages; i++ {
		logs, next, hasMore, err := r.PetSvc.ListLitterLogs(ctx, petID, summaryPageSize, cursor)
		if err != nil {
			return nil, err
		}
		reachedOlder := false
		for _, l := range logs {
			if l.Date.Before(from) {
				reachedOlder = true
				continue
			}
			if l.Date.After(to) {
				continue
			}
			all = append(all, l)
		}
		// log เรียงจากใหม่ไปเก่า เจอตัวที่เก่ากว่าช่วงที่ขอแล้วหยุดได้
		if reachedOlder || !hasMore || next == nil {
			break
		}
		cursor = next
	}
	return all, nil
}

func (r *Resolver) allWaterInRange(ctx context.Context, petID string, from, to time.Time) ([]client.WaterLog, error) {
	var all []client.WaterLog
	var cursor *string
	for i := 0; i < summaryMaxPages; i++ {
		logs, next, hasMore, err := r.PetSvc.ListWaterLogs(ctx, petID, summaryPageSize, cursor)
		if err != nil {
			return nil, err
		}
		reachedOlder := false
		for _, l := range logs {
			if l.Date.Before(from) {
				reachedOlder = true
				continue
			}
			if l.Date.After(to) {
				continue
			}
			all = append(all, l)
		}
		if reachedOlder || !hasMore || next == nil {
			break
		}
		cursor = next
	}
	return all, nil
}

// -----------------------------------------------------------------------------
// connection
// -----------------------------------------------------------------------------

func pageInfoFor(hasNext bool, end *string) *model.PageInfo {
	return &model.PageInfo{HasNextPage: hasNext, EndCursor: end}
}

func lastCursor(edges []*model.UserEdge) *string {
	if len(edges) == 0 {
		return nil
	}
	return &edges[len(edges)-1].Cursor
}

func lastPetCursor(edges []*model.PetEdge) *string {
	if len(edges) == 0 {
		return nil
	}
	return &edges[len(edges)-1].Cursor
}

func lastEventCursor(edges []*model.EventEdge) *string {
	if len(edges) == 0 {
		return nil
	}
	return &edges[len(edges)-1].Cursor
}
