// Package ai เรียก LLM ให้เขียนคำวิเคราะห์จากตัวเลขที่ service อื่นสรุปมาแล้ว
//
// อยู่ที่ BFF ไม่ใช่ในแอป เพราะ API key ที่ฝังไปกับ binary ของ mobile
// ถูกดึงออกมาได้ด้วย strings บนไฟล์ .ipa และ repo ของแอปก็เป็น public
// key หลุดเมื่อไหร่คนอื่นยิง quota แทนเจ้าของทันที
//
// ทุกอย่างในแพ็กเกจนี้ต้อง "ไม่มีก็ยังใช้งานได้" — ไม่ตั้ง key, เรียกไม่ผ่าน
// หรือเกิน quota ต้องคืน error ธรรมดาให้ resolver ตกกลับไปใช้ข้อความ
// rule-based เดิม ไม่ใช่ทำให้ทั้งหน้าจอพัง
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultEndpoint = "https://generativelanguage.googleapis.com/v1beta"

// ErrDisabled คือกรณีที่ยังไม่ได้ตั้ง API key — ไม่ใช่ความผิดพลาด
// เป็นสถานะปกติของ environment ที่ยังไม่เปิดใช้ feature นี้
var ErrDisabled = errors.New("ai: ไม่ได้ตั้ง GEMINI_API_KEY จึงยังไม่เปิดใช้")

// ErrQuota แยกจาก error อื่นเพราะ free tier มีเพดานต่อนาทีและต่อวัน
// การรู้ว่าโดน quota ต่างจาก key ผิดทำให้ดู log แล้ววินิจฉัยได้เร็ว
var ErrQuota = errors.New("ai: เกิน quota ของ Gemini")

type Gemini struct {
	apiKey   string
	model    string
	endpoint string
	http     *http.Client
}

func NewGemini(apiKey, model string, timeout time.Duration) *Gemini {
	return &Gemini{
		apiKey:   strings.TrimSpace(apiKey),
		model:    model,
		endpoint: defaultEndpoint,
		http:     &http.Client{Timeout: timeout},
	}
}

func (g *Gemini) Enabled() bool { return g != nil && g.apiKey != "" }

func (g *Gemini) Model() string { return g.model }

type generateRequest struct {
	Contents          []content        `json:"contents"`
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type content struct {
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	Temperature     float64        `json:"temperature"`
	MaxOutputTokens int            `json:"maxOutputTokens"`
	ThinkingConfig  thinkingConfig `json:"thinkingConfig"`
}

type thinkingConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type generateResponse struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// Generate ส่ง prompt ไปให้ Gemini แล้วคืนข้อความล้วน
//
// systemPrompt แยกจาก userPrompt เพราะ Gemini ให้น้ำหนักกับ systemInstruction
// ต่างจากข้อความปกติ — กติกาที่ห้ามละเมิด (ห้ามวินิจฉัยโรค) จึงอยู่ตรงนั้น
func (g *Gemini) Generate(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	if !g.Enabled() {
		return "", ErrDisabled
	}

	body, err := json.Marshal(generateRequest{
		Contents:          []content{{Parts: []part{{Text: userPrompt}}}},
		SystemInstruction: &content{Parts: []part{{Text: systemPrompt}}},
		GenerationConfig: generationConfig{
			// ต่ำแต่ไม่ศูนย์ — อยากให้สำนวนเปลี่ยนบ้างในแต่ละวัน
			// แต่ไม่อยากให้ตัวเลขหรือคำแนะนำแกว่ง
			Temperature: 0.4,
			// การ์ดบนมือถือกว้างไม่กี่บรรทัด ยาวกว่านี้ก็อ่านไม่จบ
			// ตั้งเผื่อไว้เพราะภาษาไทยกินโทเคนต่อตัวอักษรมากกว่าอังกฤษ
			MaxOutputTokens: 1024,
			// ⚠️ ปิด thinking — Gemini 3.x เปิดไว้เป็นค่าเริ่มต้นและ
			// โทเคนที่ใช้คิดถูกหักจาก MaxOutputTokens ก้อนเดียวกัน
			// ทดสอบแล้วเจอจริง: ตอบมาแค่ 12 โทเคนแล้วตัดกลางประโยคว่า
			// "วันนี้น้องมะลิเพิ่งดื่มน้ำไปได้ 9" โดย finishReason ไม่ได้บอกว่าโดนตัด
			// งานนี้เป็นการสรุปตัวเลขไม่กี่ตัว ไม่ต้องใช้ reasoning ยาวๆ อยู่แล้ว
			ThinkingConfig: thinkingConfig{ThinkingBudget: 0},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ai: ประกอบ request ไม่ได้: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", g.endpoint, g.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ai: สร้าง request ไม่ได้: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// ส่ง key ทาง header ไม่ใช่ query string — query string ไปโผล่ใน
	// access log ของ proxy ระหว่างทางได้ ส่วน header ไม่ถูกเก็บโดยปริยาย
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai: เรียก Gemini ไม่สำเร็จ: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("ai: อ่าน response ไม่ได้: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", ErrQuota
	}

	var out generateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("ai: response ไม่ใช่ JSON ที่อ่านได้ (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		// ข้อความจาก Google บอกสาเหตุตรงๆ (key ผิด, model ไม่มี, ฯลฯ)
		// เก็บไว้ใน error เพื่อให้ log บอกได้ว่าต้องไปแก้อะไร
		msg := out.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		return "", fmt.Errorf("ai: Gemini ตอบ %d: %s", resp.StatusCode, msg)
	}

	if out.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("ai: prompt ถูกบล็อก (%s)", out.PromptFeedback.BlockReason)
	}

	var b strings.Builder
	for _, c := range out.Candidates {
		for _, p := range c.Content.Parts {
			b.WriteString(p.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" {
		return "", errors.New("ai: Gemini ตอบกลับมาว่างเปล่า")
	}
	return text, nil
}
