package client

import (
	"context"
	"net/url"
	"strconv"
	"time"
)

type Pet struct {
	ID               string      `json:"id"`
	OwnerID          string      `json:"ownerId"`
	OwnerUsername    string      `json:"ownerUsername"`
	Name             string      `json:"name"`
	Species          string      `json:"species"`
	Breed            string      `json:"breed"`
	ColorCode        string      `json:"colorCode"`
	BirthDate        time.Time   `json:"birthDate"`
	Gender           string      `json:"gender"`
	CurrentWeight    *float64    `json:"currentWeight"`
	MicrochipID      *string     `json:"microchipId"`
	IsSpayedNeutered bool        `json:"isSpayedNeutered"`
	BloodType        *string     `json:"bloodType"`
	Allergies        *string     `json:"allergies"`
	Personality      *string     `json:"personality"`
	CreatedAt        time.Time   `json:"createdAt"`
	UpdatedAt        time.Time   `json:"updatedAt"`
	Caregivers       []Caregiver `json:"caregivers"`

	// AvatarData มาเฉพาะตอน PET_LIST_INCLUDE_AVATAR เปิดอยู่
	//
	// BFF ไม่ส่งต่อออกไปเด็ดขาด อ่านแค่เพื่อรู้ว่ามีรูปไหมแล้วทิ้ง
	// เพราะการส่ง base64 ออกไปคือปัญหาที่ GraphQL ตั้งใจมาแก้
	AvatarData []byte `json:"avatarData,omitempty"`
	HasAvatar  *bool  `json:"hasAvatar,omitempty"`
}

// HasAvatarValue รวมสองรูปแบบที่ REST ตอบมาให้เหลือคำตอบเดียว
func (p Pet) HasAvatarValue() bool {
	if p.HasAvatar != nil {
		return *p.HasAvatar
	}
	return len(p.AvatarData) > 0
}

type Caregiver struct {
	ID          string       `json:"id"`
	PetID       string       `json:"petId"`
	UserID      string       `json:"userId"`
	Permissions []Permission `json:"permissions"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
}

type Permission struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsActive    bool   `json:"isActive"`
}

type LitterLog struct {
	ID                string    `json:"id"`
	PetID             string    `json:"petId"`
	Date              time.Time `json:"date"`
	Type              string    `json:"type"`
	Amount            int       `json:"amount"`
	CreatedAt         time.Time `json:"createdAt"`
	CreatedByUsername *string   `json:"createdByUsername"`
	IsActive          bool      `json:"isActive"`
}

type WaterLog struct {
	ID                string    `json:"id"`
	PetID             string    `json:"petId"`
	Date              time.Time `json:"date"`
	Amount            int       `json:"amount"`
	CreatedAt         time.Time `json:"createdAt"`
	CreatedByUsername *string   `json:"createdByUsername"`
	IsActive          bool      `json:"isActive"`
}

// logPage คือรูปแบบที่ REST ตอบเมื่อส่ง limit หรือ cursor มา
type logPage[T any] struct {
	Data       []T     `json:"data"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

type MasterDataItem struct {
	Code        string    `json:"code"`
	NameEn      string    `json:"nameEn"`
	NameTh      *string   `json:"nameTh"`
	Label       string    `json:"label"`
	SpeciesCode *string   `json:"speciesCode"`
	SortOrder   int       `json:"sortOrder"`
	IsActive    bool      `json:"isActive"`
	Version     int       `json:"version"`
	UpdatedAt   time.Time `json:"updatedAt"`
	UpdatedBy   *string   `json:"updatedBy"`
}

type PetClient struct{ h *HTTP }

func NewPetClient(base string, timeout time.Duration) *PetClient {
	return &PetClient{h: NewHTTP(base, timeout)}
}

func (c *PetClient) ListPets(ctx context.Context) ([]Pet, error) {
	var out []Pet
	err := c.h.Do(ctx, "GET", "/api/v1/pets", nil, nil, &out)
	return out, err
}

func (c *PetClient) AdminListPets(ctx context.Context) ([]Pet, error) {
	var out []Pet
	err := c.h.Do(ctx, "GET", "/api/v1/admin/pets", nil, nil, &out)
	return out, err
}

func (c *PetClient) GetPet(ctx context.Context, id string) (*Pet, error) {
	var out Pet
	if err := c.h.Do(ctx, "GET", "/api/v1/pets/"+id, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) CreatePet(ctx context.Context, body any) (*Pet, error) {
	var out Pet
	if err := c.h.Do(ctx, "POST", "/api/v1/pets", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) UpdatePet(ctx context.Context, id string, body any) (*Pet, error) {
	var out Pet
	if err := c.h.Do(ctx, "PUT", "/api/v1/pets/"+id, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) DeletePet(ctx context.Context, id string) error {
	return c.h.Do(ctx, "DELETE", "/api/v1/pets/"+id, nil, nil, nil)
}

func (c *PetClient) ListCaregivers(ctx context.Context, petID string) ([]Caregiver, error) {
	var out []Caregiver
	err := c.h.Do(ctx, "GET", "/api/v1/pets/"+petID+"/caregivers", nil, nil, &out)
	return out, err
}

func (c *PetClient) AddCaregiver(ctx context.Context, petID, userID string) (*Caregiver, error) {
	var out Caregiver
	body := map[string]string{"userId": userID}
	if err := c.h.Do(ctx, "POST", "/api/v1/pets/"+petID+"/caregivers", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) UpdateCaregiverPermissions(ctx context.Context, petID, caregiverID string, permissionIDs []string) (*Caregiver, error) {
	var out Caregiver
	body := map[string]any{"permissionIds": permissionIDs}
	if err := c.h.Do(ctx, "PUT", "/api/v1/pets/"+petID+"/caregivers/"+caregiverID, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) RemoveCaregiver(ctx context.Context, petID, caregiverID string) error {
	return c.h.Do(ctx, "DELETE", "/api/v1/pets/"+petID+"/caregivers/"+caregiverID, nil, nil, nil)
}

// ListLitterLogs ขอแบบแบ่งหน้าเสมอ
//
// REST จะตอบเป็น array เปล่าถ้าไม่ส่ง limit มา ซึ่งเป็นคนละรูปแบบกัน
// การส่ง limit ทุกครั้งทำให้ได้รูปแบบเดียวและกันไม่ให้เผลอดึงทั้งประวัติ
func (c *PetClient) ListLitterLogs(ctx context.Context, petID string, limit int, cursor *string) ([]LitterLog, *string, bool, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if cursor != nil && *cursor != "" {
		q.Set("cursor", *cursor)
	}
	var out logPage[LitterLog]
	if err := c.h.Do(ctx, "GET", "/api/v1/pets/"+petID+"/litter-logs", q, nil, &out); err != nil {
		return nil, nil, false, err
	}
	return out.Data, out.NextCursor, out.HasMore, nil
}

func (c *PetClient) ListWaterLogs(ctx context.Context, petID string, limit int, cursor *string) ([]WaterLog, *string, bool, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if cursor != nil && *cursor != "" {
		q.Set("cursor", *cursor)
	}
	var out logPage[WaterLog]
	if err := c.h.Do(ctx, "GET", "/api/v1/pets/"+petID+"/water-logs", q, nil, &out); err != nil {
		return nil, nil, false, err
	}
	return out.Data, out.NextCursor, out.HasMore, nil
}

func (c *PetClient) CreateLitterLog(ctx context.Context, petID string, body any) (*LitterLog, error) {
	var out LitterLog
	if err := c.h.Do(ctx, "POST", "/api/v1/pets/"+petID+"/litter-logs", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) CreateLitterLogBatch(ctx context.Context, petID string, body any) ([]LitterLog, error) {
	var out []LitterLog
	err := c.h.Do(ctx, "POST", "/api/v1/pets/"+petID+"/litter-logs/batch", nil, body, &out)
	return out, err
}

func (c *PetClient) DeleteLitterLog(ctx context.Context, petID, logID string) error {
	return c.h.Do(ctx, "DELETE", "/api/v1/pets/"+petID+"/litter-logs/"+logID, nil, nil, nil)
}

func (c *PetClient) CreateWaterLog(ctx context.Context, petID string, body any) (*WaterLog, error) {
	var out WaterLog
	if err := c.h.Do(ctx, "POST", "/api/v1/pets/"+petID+"/water-logs", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) DeleteWaterLog(ctx context.Context, petID, logID string) error {
	return c.h.Do(ctx, "DELETE", "/api/v1/pets/"+petID+"/water-logs/"+logID, nil, nil, nil)
}

func (c *PetClient) AdminCreateMasterData(ctx context.Context, typ string, body any) (*MasterDataItem, error) {
	var out MasterDataItem
	if err := c.h.Do(ctx, "POST", "/api/v1/admin/master-data/"+typ, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *PetClient) AdminUpdateMasterData(ctx context.Context, typ, code string, body any) (*MasterDataItem, error) {
	var out MasterDataItem
	if err := c.h.Do(ctx, "PUT", "/api/v1/admin/master-data/"+typ+"/"+code, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// AdminDeactivateMasterData — DELETE ตัวนี้ไม่ได้ลบจริง แค่ปิดการใช้งาน
// และตอบ 200 พร้อม body ต่างจาก DELETE อื่นที่ตอบ 204 เปล่า
func (c *PetClient) AdminDeactivateMasterData(ctx context.Context, typ, code string) error {
	return c.h.Do(ctx, "DELETE", "/api/v1/admin/master-data/"+typ+"/"+code, nil, nil, nil)
}

func (c *PetClient) AdminListMasterData(ctx context.Context, typ string) ([]MasterDataItem, error) {
	var out []MasterDataItem
	err := c.h.Do(ctx, "GET", "/api/v1/admin/master-data/"+typ, nil, nil, &out)
	return out, err
}
