package graph

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vertex/bff/internal/adapter"
	"github.com/vertex/bff/internal/graph/model"
)

// -----------------------------------------------------------------------------
// แปลง DTO ของ REST เป็น model ของ GraphQL
// -----------------------------------------------------------------------------

func toPet(p adapter.Pet) *model.Pet {
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

func toCaregiver(c adapter.Caregiver) *model.PetCaregiver {
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

func toUser(u adapter.User) *model.User {
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

func toLitterLog(l adapter.LitterLog) *model.LitterLog {
	return &model.LitterLog{
		ID: l.ID, PetID: l.PetID, Date: l.Date, Type: l.Type, Amount: l.Amount,
		CreatedByUsername: l.CreatedByUsername, CreatedAt: l.CreatedAt,
	}
}

func toWaterLog(l adapter.WaterLog) *model.WaterLog {
	return &model.WaterLog{
		ID: l.ID, PetID: l.PetID, Date: l.Date, Amount: l.Amount,
		CreatedByUsername: l.CreatedByUsername, CreatedAt: l.CreatedAt,
	}
}

func toEvent(e adapter.Event) *model.Event {
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

func toMasterDataItem(m adapter.MasterDataItem) *model.MasterDataItem {
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
func viewerPermissions(pet adapter.Pet, viewerID string, roles []string) *model.PetViewerPermissions {
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
