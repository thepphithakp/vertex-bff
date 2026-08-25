package model

import (
	"encoding/json"
	"fmt"
	"io"
)

// JSON คือ payload ของ event ที่รูปร่างไม่แน่นอน
//
// เก็บเป็น raw message แล้วส่งต่อตามเดิม ไม่แกะเป็น map
// เพราะ BFF ไม่ได้เป็นเจ้าของรูปแบบนี้ — service ต้นทางเป็นคนกำหนด
type JSON json.RawMessage

func (j JSON) MarshalGQL(w io.Writer) {
	if len(j) == 0 {
		_, _ = w.Write([]byte("null"))
		return
	}
	_, _ = w.Write(j)
}

func (j *JSON) UnmarshalGQL(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("payload ไม่ใช่ JSON ที่ถูกต้อง: %w", err)
	}
	*j = b
	return nil
}
