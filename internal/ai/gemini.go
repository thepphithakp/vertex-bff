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
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultEndpoint = "https://generativelanguage.googleapis.com/v1beta"

// cooldown ที่ใช้เมื่อ Gemini ไม่ได้บอก retryDelay มาด้วย
//
// เดา 5 นาทีเพราะเพดานของ free tier มีทั้งแบบต่อนาทีและต่อวัน ถ้าเป็นแบบต่อวัน
// การลองใหม่เร็วกว่านี้ก็เสียเที่ยวเปล่า แต่ไม่ได้ทำให้อะไรพัง เพราะสุดท้าย
// มีโมเดลถัดไปกับข้อความสำรองรออยู่
const defaultCooldown = 5 * time.Minute

// ErrDisabled คือกรณีที่ยังไม่ได้ตั้ง API key — ไม่ใช่ความผิดพลาด
// เป็นสถานะปกติของ environment ที่ยังไม่เปิดใช้ feature นี้
var ErrDisabled = errors.New("ai: ไม่ได้ตั้ง GEMINI_API_KEY จึงยังไม่เปิดใช้")

// ErrQuota แปลว่าลองครบทุกโมเดลที่ตั้งไว้แล้วโดนเพดานหมด
//
// แยกจาก error อื่นเพราะเวลาอ่าน log แล้ว "หมดโควตา" กับ "key ผิด"
// เป็นคนละเรื่องกันโดยสิ้นเชิงเวลาต้องไปแก้
var ErrQuota = errors.New("ai: เกิน quota ของ Gemini ครบทุกโมเดลที่ตั้งไว้")

type Gemini struct {
	apiKey string

	// models เรียงตามลำดับที่จะลอง ตัวแรกคือตัวหลัก
	//
	// โมเดลแต่ละตัวมีโควตาแยกก้อนกัน การมีหลายตัวจึงเพิ่มจำนวนครั้งที่ใช้ได้จริง
	// ไม่ใช่แค่กันพลาด — flash-lite ได้ 15 RPM / 500 RPD ส่วน 3.5-flash ได้
	// 5 RPM / 20 RPD ต่างกัน 25 เท่าต่อวัน
	models   []string
	endpoint string
	http     *http.Client

	mu       sync.Mutex
	coolDown map[string]time.Time
	// noThinkingConfig จำว่าโมเดลไหนไม่รับ thinkingConfig
	//
	// gemini-3.5-flash-lite ตอบ 400 ถ้าส่งไปด้วย ส่วน 3.1-flash-lite กับ
	// 3.5-flash รับได้ปกติ — เรียนเอาตอน runtime ดีกว่าเขียนรายชื่อไว้ใน code
	// เพราะรายการโมเดลของ Google เปลี่ยนบ่อยกว่าที่เราจะตามแก้ไหว
	//
	// ปลอดภัยที่จะไม่ส่ง เพราะตัวที่ไม่รับคือตัวที่ไม่มี thinking ให้ปิดอยู่แล้ว
	// (ทดสอบแล้ว lite ตอบครบประโยคโดยไม่ต้องปิดอะไร)
	noThinkingConfig map[string]bool
	now              func() time.Time
}

func NewGemini(apiKey string, models []string, timeout time.Duration) *Gemini {
	cleaned := make([]string, 0, len(models))
	for _, m := range models {
		if m = strings.TrimSpace(m); m != "" {
			cleaned = append(cleaned, m)
		}
	}
	return &Gemini{
		apiKey:           strings.TrimSpace(apiKey),
		models:           cleaned,
		endpoint:         defaultEndpoint,
		http:             &http.Client{Timeout: timeout},
		coolDown:         make(map[string]time.Time),
		noThinkingConfig: make(map[string]bool),
		now:              time.Now,
	}
}

func (g *Gemini) Enabled() bool { return g != nil && g.apiKey != "" && len(g.models) > 0 }

// Models คืนลำดับที่ตั้งไว้ ใช้ตอนเขียน log ตอน service start
func (g *Gemini) Models() []string { return g.models }

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
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
	// pointer เพื่อให้ omitempty ตัดทิ้งได้จริงเมื่อโมเดลไม่รับ
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
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
		Details []struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay"`
		} `json:"details"`
	} `json:"error"`
}

// Generate ไล่ลองโมเดลตามลำดับจนกว่าจะมีตัวใดตัวหนึ่งตอบ
//
// คืนชื่อโมเดลที่ตอบจริงมาด้วย เพราะข้อความที่ผู้ใช้เห็นอาจมาจากตัวสำรอง
// เวลาสำนวนเปลี่ยนไปจะได้ตอบได้ว่ามาจากไหน
func (g *Gemini) Generate(ctx context.Context, systemPrompt, userPrompt string) (string, string, error) {
	if !g.Enabled() {
		return "", "", ErrDisabled
	}

	var (
		attempted int
		quotaHit  bool
		lastErr   error
	)

	for _, m := range g.models {
		// ตัวที่เพิ่งโดนเพดานไปข้ามไปเลย ไม่ต้องเสียเวลายิงให้โดน 429 ซ้ำ
		if g.cooling(m) {
			continue
		}
		attempted++

		text, err := g.generateWith(ctx, m, systemPrompt, userPrompt, g.wantsThinkingConfig(m))
		if errors.Is(err, errThinkingConfigRejected) {
			// โมเดลนี้ไม่รับ thinkingConfig — จำไว้แล้วลองใหม่ทันทีโดยไม่ส่ง
			// ไม่นับเป็นความล้มเหลวของโมเดล เพราะเป็นเรื่องของ request ที่เราส่ง
			g.markNoThinkingConfig(m)
			slog.InfoContext(ctx, "โมเดลไม่รับ thinkingConfig ลองใหม่โดยไม่ส่ง", "model", m)
			text, err = g.generateWith(ctx, m, systemPrompt, userPrompt, false)
		}
		if err == nil {
			return text, m, nil
		}
		lastErr = err

		var qe *quotaError
		if errors.As(err, &qe) {
			quotaHit = true
			g.markCooling(m, qe.retryAfter)
			slog.WarnContext(ctx, "โมเดลหมดโควตา สลับไปตัวถัดไป",
				"model", m, "cooldown", g.cooldownOf(m).String())
			continue
		}

		// 404 (โมเดลนี้ใช้กับ key นี้ไม่ได้) และ 5xx ลองตัวถัดไปมีโอกาสรอด
		// ส่วน prompt ที่ถูกบล็อกจะโดนเหมือนกันทุกตัว ไม่ต้องลองต่อ
		if !worthRetryingOnNextModel(err) {
			return "", "", err
		}
		slog.WarnContext(ctx, "โมเดลใช้ไม่ได้ สลับไปตัวถัดไป", "model", m, "error", err.Error())
	}

	// ไม่ได้ยิงเลยเพราะทุกตัวยัง cooldown อยู่ ก็นับเป็นหมดโควตาเหมือนกัน
	if attempted == 0 || quotaHit {
		return "", "", ErrQuota
	}
	return "", "", lastErr
}

type quotaError struct {
	retryAfter time.Duration
}

func (e *quotaError) Error() string {
	return fmt.Sprintf("ai: เกิน quota (Google บอกให้รอ %s)", e.retryAfter)
}

// retryableError คือความผิดพลาดที่ "ลองโมเดลอื่นแล้วอาจรอด"
type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

func worthRetryingOnNextModel(err error) bool {
	var re *retryableError
	return errors.As(err, &re)
}

func (g *Gemini) cooling(model string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.coolDown[model]
	return ok && g.now().Before(until)
}

func (g *Gemini) markCooling(model string, d time.Duration) {
	if d <= 0 {
		d = defaultCooldown
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.coolDown[model] = g.now().Add(d)
}

// errThinkingConfigRejected แยกจาก error 400 อื่น เพราะมีทางแก้ที่ชัดเจน
// คือลองใหม่โดยไม่ส่ง field นั้น ไม่ใช่ยอมแพ้แล้วข้ามไปโมเดลอื่น
var errThinkingConfigRejected = errors.New("ai: โมเดลนี้ไม่รับ thinkingConfig")

func (g *Gemini) wantsThinkingConfig(model string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.noThinkingConfig[model]
}

func (g *Gemini) markNoThinkingConfig(model string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.noThinkingConfig[model] = true
}

func (g *Gemini) cooldownOf(model string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.coolDown[model].Sub(g.now())
}

func (g *Gemini) generateWith(ctx context.Context, model, systemPrompt, userPrompt string, withThinkingConfig bool) (string, error) {
	cfg := generationConfig{
		// ⚠️ อย่าขยับขึ้นเกินนี้ — โมเดล lite เขียนไทยเพี้ยนเมื่อ temperature สูง
		//
		// วัดจริงด้วย prompt เดียวกัน:
		//   0.8  "ยังมีวันกราบน้ำไม่ถึงเกณฑ์" · "เปลี่ยนมาใช้พุ 1 น้ำพุแมว"
		//        และชื่อแมวขาดตัว "ส้มโอ" กลายเป็น "ส้ม "
		//   0.5  พูดผิดแล้วแก้กลางประโยค "วันนี้ขนเพชร... เอ๊ย ขนมปัง"
		//   0.3  สะอาดทุกตัวอย่าง
		//
		// ความหลากหลายของสำนวนมาจาก angle ใน insight.go ไม่ใช่จากค่านี้
		// การดันค่านี้ขึ้นแลกมาด้วยคำที่อ่านแล้วสะดุด ซึ่งแย่กว่าสำนวนซ้ำ
		Temperature: 0.35,
		// การ์ดบนมือถือกว้างไม่กี่บรรทัด ยาวกว่านี้ก็อ่านไม่จบ
		// ตั้งเผื่อไว้เพราะภาษาไทยกินโทเคนต่อตัวอักษรมากกว่าอังกฤษ
		MaxOutputTokens: 1024,
		// ⚠️ ปิด thinking — Gemini 3.x เปิดไว้เป็นค่าเริ่มต้นและโทเคนที่ใช้คิด
		// ถูกหักจาก MaxOutputTokens ก้อนเดียวกัน ทดสอบแล้วเจอจริง:
		// ตอบมาแค่ 12 โทเคนแล้วตัดกลางประโยคว่า "วันนี้น้องมะลิเพิ่งดื่มน้ำไปได้ 9"
		// โดย finishReason ไม่ได้บอกว่าโดนตัด
		// งานนี้เป็นการสรุปตัวเลขไม่กี่ตัว ไม่ต้องใช้ reasoning ยาวๆ อยู่แล้ว
	}
	if withThinkingConfig {
		cfg.ThinkingConfig = &thinkingConfig{ThinkingBudget: 0}
	}

	body, err := json.Marshal(generateRequest{
		Contents:          []content{{Parts: []part{{Text: userPrompt}}}},
		SystemInstruction: &content{Parts: []part{{Text: systemPrompt}}},
		GenerationConfig:  cfg,
	})
	if err != nil {
		return "", fmt.Errorf("ai: ประกอบ request ไม่ได้: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", g.endpoint, model)
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
		// network สะดุดตอนคุยกับโมเดลนี้ ลองตัวถัดไปได้
		return "", &retryableError{fmt.Errorf("ai: เรียก %s ไม่สำเร็จ: %w", model, err)}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", &retryableError{fmt.Errorf("ai: อ่าน response ของ %s ไม่ได้: %w", model, err)}
	}

	var out generateResponse
	if jsonErr := json.Unmarshal(raw, &out); jsonErr != nil && resp.StatusCode == http.StatusOK {
		return "", fmt.Errorf("ai: response ของ %s ไม่ใช่ JSON ที่อ่านได้", model)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", &quotaError{retryAfter: out.retryDelay()}
	}

	if resp.StatusCode != http.StatusOK {
		msg := out.Error.Message
		if msg == "" {
			msg = strings.TrimSpace(string(raw))
		}
		httpErr := fmt.Errorf("ai: %s ตอบ %d: %s", model, resp.StatusCode, msg)
		// 400 ตอนที่ส่ง thinkingConfig ไป = โมเดลนี้ไม่รู้จัก field นั้น
		// (เจอจริงกับ gemini-3.5-flash-lite) ลองใหม่โดยไม่ส่งได้เลย
		if resp.StatusCode == http.StatusBadRequest && withThinkingConfig {
			return "", errThinkingConfigRejected
		}
		// 404 เจอจริงกับ gemini-2.5-flash ที่มีชื่อในรายการ models แต่เรียกไม่ได้
		// 5xx เป็นฝั่ง Google เอง — ทั้งสองแบบลองตัวถัดไปมีโอกาสรอด
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode >= 500 {
			return "", &retryableError{httpErr}
		}
		return "", httpErr
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
		return "", &retryableError{fmt.Errorf("ai: %s ตอบกลับมาว่างเปล่า", model)}
	}
	return text, nil
}

// retryDelay อ่านเวลาที่ Google บอกว่าให้รอก่อนลองใหม่
//
// มากับ error details เป็น RetryInfo เช่น "26s" ใช้ค่านี้ดีกว่าเดาเอง
// เพราะเพดานต่อนาทีกับต่อวันมีเวลารอต่างกันมาก
func (r generateResponse) retryDelay() time.Duration {
	for _, d := range r.Error.Details {
		if d.RetryDelay == "" {
			continue
		}
		if v, err := time.ParseDuration(d.RetryDelay); err == nil {
			return v
		}
	}
	return 0
}
