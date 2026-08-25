package graph

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vertex/bff/internal/client"
	"github.com/vertex/bff/internal/graph/model"
)

// -----------------------------------------------------------------------------
// แปลง DTO ของ REST เป็น model ของ GraphQL
// -----------------------------------------------------------------------------

func toPet(p client.Pet) *model.Pet {
	return &model.Pet{
		ID:               p.ID,
		Name:             p.Name,
		Species:          p.Species,
		Breed:            p.Breed,
		ColorCode:        p.ColorCode,
		BirthDate:        p.BirthDate,
		Gender:           p.Gender,
		CurrentWeight:    p.CurrentWeight,
		MicrochipID:      p.MicrochipID,
		IsSpayedNeutered: p.IsSpayedNeutered,
		BloodType:        p.BloodType,
		Allergies:        p.Allergies,
		Personality:      p.Personality,
		HasAvatar:        p.HasAvatarValue(),
		CreatedAt:        p.CreatedAt,
		UpdatedAt:        p.UpdatedAt,
	}
}

func toCaregiver(c client.Caregiver) *model.PetCaregiver {
	perms := make([]*model.PetPermission, 0, len(c.Permissions))
	for _, p := range c.Permissions {
		perms = append(perms, &model.PetPermission{
			ID: p.ID, Name: p.Name, Description: p.Description, IsActive: p.IsActive,
		})
	}
	return &model.PetCaregiver{
		ID:          c.ID,
		PetID:       c.PetID,
		Permissions: perms,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

func toUser(u client.User) *model.User {
	return &model.User{
		ID:            u.ID,
		Email:         u.Email,
		FullName:      u.FullName,
		EmailVerified: u.EmailVerified,
		Roles:         toRoles(u.Roles),
	}
}

func toRoles(codes []string) []model.Role {
	if len(codes) == 0 {
		return nil
	}
	out := make([]model.Role, 0, len(codes))
	for _, c := range codes {
		r := model.Role(c)
		if r.IsValid() {
			out = append(out, r)
		}
	}
	return out
}

func toLitterLog(l client.LitterLog) *model.LitterLog {
	return &model.LitterLog{
		ID: l.ID, PetID: l.PetID, Date: l.Date, Type: l.Type, Amount: l.Amount,
		CreatedByUsername: l.CreatedByUsername, CreatedAt: l.CreatedAt,
	}
}

func toWaterLog(l client.WaterLog) *model.WaterLog {
	return &model.WaterLog{
		ID: l.ID, PetID: l.PetID, Date: l.Date, Amount: l.Amount,
		CreatedByUsername: l.CreatedByUsername, CreatedAt: l.CreatedAt,
	}
}

func toEvent(e client.Event) *model.Event {
	ev := &model.Event{
		ID: e.ID, Timestamp: e.Timestamp, EventType: e.EventType, Action: e.Action,
	}
	if e.ActorID != "" {
		ev.ActorID = &e.ActorID
	}
	if e.ActorUsername != "" {
		ev.ActorUsername = &e.ActorUsername
	}
	if e.EntityType != "" {
		ev.EntityType = &e.EntityType
	}
	if e.EntityID != "" {
		ev.EntityID = &e.EntityID
	}
	if len(e.Payload) > 0 {
		ev.Payload = model.JSON(e.Payload)
	}
	return ev
}

func toMasterDataItem(m client.MasterDataItem) *model.MasterDataItem {
	return &model.MasterDataItem{
		Code: m.Code, NameEn: m.NameEn, NameTh: m.NameTh, Label: m.Label,
		SpeciesCode: m.SpeciesCode, SortOrder: m.SortOrder, IsActive: m.IsActive,
		Version: m.Version, UpdatedAt: m.UpdatedAt, UpdatedBy: m.UpdatedBy,
	}
}

// masterDataPath แปลงชื่อ enum เป็น path ที่ REST ใช้
//
// enum ใช้ SCREAMING_SNAKE ตามธรรมเนียม GraphQL ส่วน REST ใช้ kebab-case
// จุดแปลงต้องอยู่ที่เดียวคือที่นี่ ไม่ใช่กระจายอยู่ในแต่ละ resolver
func masterDataPath(t model.MasterDataType) string {
	switch t {
	case model.MasterDataTypeSpecies:
		return "species"
	case model.MasterDataTypeCatBreeds:
		return "cat-breeds"
	case model.MasterDataTypeBloodTypes:
		return "blood-types"
	case model.MasterDataTypeLitterTypes:
		return "litter-types"
	case model.MasterDataTypeGenders:
		return "genders"
	default:
		return strings.ToLower(string(t))
	}
}

// -----------------------------------------------------------------------------
// อายุ
// -----------------------------------------------------------------------------

// ageLabel คำนวณอายุแบบอ่านง่าย ย้ายมาจากที่ client เคยคำนวณเอง
//
// ทั้ง iOS และ back office เคยมีสูตรของตัวเอง ซึ่งแปลว่ามีโอกาสแสดงไม่ตรงกัน
func ageLabel(birth time.Time, now time.Time) string {
	if birth.IsZero() || birth.After(now) {
		return "ไม่ระบุ"
	}
	years := now.Year() - birth.Year()
	months := int(now.Month()) - int(birth.Month())
	if now.Day() < birth.Day() {
		months--
	}
	if months < 0 {
		years--
		months += 12
	}
	switch {
	case years <= 0 && months <= 0:
		return "น้อยกว่า 1 เดือน"
	case years <= 0:
		return fmt.Sprintf("%d เดือน", months)
	case months == 0:
		return fmt.Sprintf("%d ปี", years)
	default:
		return fmt.Sprintf("%d ปี %d เดือน", years, months)
	}
}

// -----------------------------------------------------------------------------
// สิทธิ์
// -----------------------------------------------------------------------------

const (
	permEditProfile  = "EDIT_PROFILE"
	permManageLitter = "MANAGE_LITTER"
	permManageWater  = "MANAGE_WATER"
)

// viewerPermissions บอกว่าผู้เรียกทำอะไรกับสัตว์เลี้ยงตัวนี้ได้บ้าง
//
// ⚠️ นี่คือข้อมูลไว้ให้หน้าจอตัดสินใจว่าจะโชว์ปุ่มไหม **ไม่ใช่การบังคับสิทธิ์**
//
//	การบังคับจริงอยู่ที่ชั้น service ของ pet-service เสมอ ถ้าใครยิง mutation
//	ตรงๆ โดยไม่ผ่านหน้าจอ ก็จะโดนปฏิเสธที่นั่นอยู่ดี
//	ห้ามย้ายการตัดสินใจเรื่องสิทธิ์มาไว้ที่นี่แล้วเอาออกจาก service
func viewerPermissions(pet client.Pet, viewerID string, roles []string) *model.PetViewerPermissions {
	isOwner := strings.EqualFold(pet.OwnerID, viewerID)
	isAdmin := hasRole(roles, "SUPER_ADMIN") || hasRole(roles, "PET_ADMIN")

	granted := map[string]bool{}
	for _, cg := range pet.Caregivers {
		if !strings.EqualFold(cg.UserID, viewerID) {
			continue
		}
		for _, p := range cg.Permissions {
			if p.IsActive {
				granted[p.ID] = true
			}
		}
	}

	return &model.PetViewerPermissions{
		IsOwner:             isOwner,
		CanEditProfile:      isOwner || isAdmin || granted[permEditProfile],
		CanManageCaregivers: isOwner || isAdmin,
		CanManageLitter:     isOwner || isAdmin || granted[permManageLitter],
		CanManageWater:      isOwner || isAdmin || granted[permManageWater],
		CanDelete:           isOwner || isAdmin,
	}
}

func hasRole(roles []string, want string) bool {
	for _, r := range roles {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

// -----------------------------------------------------------------------------
// สรุปรายวัน
// -----------------------------------------------------------------------------

// dayKey ตัดเวลาออกให้เหลือแค่วัน ใช้เป็น key ของ bucket
func dayKey(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// eachDay ไล่ทุกวันตั้งแต่ from ถึง to
//
// ต้องเติมวันที่ไม่มีข้อมูลเป็น 0 ด้วย ไม่งั้นกราฟจะขาดช่วงและอ่านผิด
func eachDay(from, to time.Time, fn func(time.Time)) {
	for d := dayKey(from); !d.After(dayKey(to)); d = d.AddDate(0, 0, 1) {
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

func buildLitterSummary(logs []client.LitterLog, from, to time.Time) *model.LitterSummary {
	poop := map[time.Time]int{}
	pee := map[time.Time]int{}
	totalPoop, totalPee := 0, 0

	for _, l := range logs {
		if !l.IsActive {
			continue
		}
		k := dayKey(l.Date)
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

func buildWaterSummary(logs []client.WaterLog, from, to time.Time, weightKg *float64) *model.WaterSummary {
	ml := map[time.Time]int{}
	total := 0
	for _, l := range logs {
		if !l.IsActive {
			continue
		}
		ml[dayKey(l.Date)] += l.Amount
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

// -----------------------------------------------------------------------------
// cursor
// -----------------------------------------------------------------------------
//
// cursor เป็นค่าทึบตามสัญญาใน schema — client ห้ามตีความหรือสร้างเอง
// ที่นี่ใช้เข้ารหัส offset สำหรับ endpoint ที่ REST ยังเป็น offset อยู่

func encodeOffset(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(offset)))
}

func decodeOffset(cursor *string) (int, error) {
	if cursor == nil || *cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(*cursor)
	if err != nil {
		return 0, fmt.Errorf("cursor ไม่ถูกต้อง")
	}
	s := string(raw)
	if !strings.HasPrefix(s, "o:") {
		return 0, fmt.Errorf("cursor ไม่ถูกต้อง")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(s, "o:"))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("cursor ไม่ถูกต้อง")
	}
	return n, nil
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

func pageSize(first *int) int {
	if first == nil || *first <= 0 {
		return defaultPageSize
	}
	if *first > maxPageSize {
		return maxPageSize
	}
	return *first
}
