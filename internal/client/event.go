package client

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
)

type Event struct {
	ID            string          `json:"id"`
	Timestamp     time.Time       `json:"timestamp"`
	CreatedAt     time.Time       `json:"createdAt"`
	EventType     string          `json:"eventType"`
	Action        string          `json:"action"`
	ActorID       string          `json:"actorId"`
	ActorUsername string          `json:"actorUsername"`
	EntityID      string          `json:"entityId"`
	EntityType    string          `json:"entityType"`
	Payload       json.RawMessage `json:"payload"`
}

type EventPage struct {
	Data []Event `json:"data"`

	// Total คือจำนวนทั้งหมดที่ตรงเงื่อนไข ใช้ทำ totalCount ได้
	Total int64 `json:"total"`

	// ⚠️ Limit ที่ REST ตอบกลับมาคือ "จำนวนแถวที่ได้จริง" ไม่ใช่ขนาดหน้าที่ขอ
	//    ห้ามเอาไปใช้คำนวณหน้าถัดไป ให้ใช้ค่าที่เราส่งไปเองแทน
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type EventFilter struct {
	EntityType string
	EntityID   string
	ActorID    string
	Limit      int
	Offset     int
}

type EventClient struct{ h *HTTP }

func NewEventClient(base string, timeout time.Duration) *EventClient {
	return &EventClient{h: NewHTTP(base, timeout)}
}

func (c *EventClient) AdminListEvents(ctx context.Context, f EventFilter) (*EventPage, error) {
	q := url.Values{}
	if f.EntityType != "" {
		q.Set("entityType", f.EntityType)
	}
	if f.EntityID != "" {
		q.Set("entityId", f.EntityID)
	}
	if f.ActorID != "" {
		q.Set("actorId", f.ActorID)
	}
	q.Set("limit", strconv.Itoa(f.Limit))
	q.Set("offset", strconv.Itoa(f.Offset))

	var out EventPage
	if err := c.h.Do(ctx, "GET", "/api/v1/admin/events", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
