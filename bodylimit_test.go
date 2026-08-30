package main

import (
	"bytes"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/adaptor/v2"
	"github.com/gofiber/fiber/v2"
)

// serve เปิด app บน listener จริงแล้วคืน base URL
//
// ต้องยิงผ่าน TCP จริงไม่ใช่ app.Test เพราะเพดาน body ถูกบังคับที่ชั้น fasthttp
// ตอนอ่าน socket — app.Test คืน error ออกมาแทนที่จะให้ response ทำให้พิสูจน์
// ไม่ได้ว่า "client ได้ 413 จริงไหม" ซึ่งคือสิ่งที่เราสนใจ
func serve(t *testing.T, app *fiber.App) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("เปิด listener ไม่ได้: %v", err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	addr := ln.Addr().String()
	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return "http://" + addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server ไม่ขึ้นภายในเวลาที่รอ")
	return ""
}

// TestBodyLimitRejectsBeforeHandler เป็นข้อกำหนดหลักของ VT-128
//
// ไม่พอที่จะปฏิเสธ — ต้องปฏิเสธ **ก่อน** handler ได้เห็น body ไม่งั้นคนที่ไม่มี
// token ก็ยังทำให้ pod อ่าน body ทั้งก้อนเข้าหน่วยความจำได้อยู่ดี ซึ่งเป็นตัว
// ปัญหาจริง (pod มี limits.memory 256Mi ส่วน Cloudflare ปล่อย body ได้ 100MB)
//
// วัดด้วยจำนวนครั้งที่ handler ถูกเรียก ซึ่งต้องเป็นศูนย์
func TestBodyLimitRejectsBeforeHandler(t *testing.T) {
	var called atomic.Int32

	app := newFiberApp()
	app.All("/graphql", adaptor.HTTPHandler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			w.WriteHeader(http.StatusOK)
		})))

	base := serve(t, app)

	// ใช้ Expect: 100-continue เพื่อให้ผลลัพธ์คงที่
	//
	// ถ้าส่ง body ตรงๆ server จะตอบ 413 แล้วปิด connection ทันทีขณะที่ client
	// ยังเขียนอยู่ ทำให้ได้ broken pipe บ้าง ได้ 413 บ้าง แล้วแต่จังหวะ
	//
	// ผลพลอยได้: พิสูจน์ด้วยว่า body ไม่ถูกส่งข้าม network เลยแม้แต่ไบต์เดียว
	// ซึ่งเป็นพฤติกรรมที่อยากได้จริงๆ
	req, err := http.NewRequest(http.MethodPost, base+"/graphql",
		bytes.NewReader(make([]byte, bodyLimit+1)))
	if err != nil {
		t.Fatalf("สร้าง request ไม่ได้: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Expect", "100-continue")

	client := &http.Client{Transport: &http.Transport{
		ExpectContinueTimeout: 5 * time.Second,
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("ยิง request ไม่สำเร็จ: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fiber.StatusRequestEntityTooLarge {
		t.Errorf("อยากได้ 413 แต่ได้ %d", resp.StatusCode)
	}
	if n := called.Load(); n != 0 {
		t.Errorf("handler ต้องไม่ถูกเรียกเลย แต่ถูกเรียก %d ครั้ง — "+
			"แปลว่า body ถูกอ่านเข้าหน่วยความจำก่อนถูกปฏิเสธ", n)
	}
}

// TestBodyLimitAllowsRealOperations — เพดานต้องไม่เล็กจนของจริงใช้ไม่ได้
//
// operation ที่ใหญ่สุดคือ logLitterBatch ที่รับ array ของ log
// จำลองด้วย batch 500 รายการซึ่งมากกว่าที่แอปส่งจริงหลายเท่า
func TestBodyLimitAllowsRealOperations(t *testing.T) {
	var called atomic.Int32

	app := newFiberApp()
	app.All("/graphql", adaptor.HTTPHandler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			called.Add(1)
			w.WriteHeader(http.StatusOK)
		})))

	var b strings.Builder
	b.WriteString(`{"query":"mutation{logLitterBatch(petId:\"x\",inputs:[`)
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{type:\"Poop\",loggedAt:\"2026-08-30T00:00:00Z\",note:\"ปกติดี\"}`)
	}
	b.WriteString(`]){id}}"}`)

	if b.Len() >= bodyLimit {
		t.Fatalf("batch 500 รายการ (%d ไบต์) ใหญ่กว่าเพดาน %d — "+
			"ถ้าของจริงโตขนาดนี้ ต้องทบทวนค่า bodyLimit ไม่ใช่แก้ test", b.Len(), bodyLimit)
	}

	base := serve(t, app)

	resp, err := http.Post(base+"/graphql", "application/json", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ยิง request ไม่สำเร็จ: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == fiber.StatusRequestEntityTooLarge {
		t.Errorf("operation ปกติขนาด %d ไบต์ ถูกปฏิเสธด้วย 413 — เพดานเล็กเกินไป", b.Len())
	}
	if called.Load() != 1 {
		t.Errorf("handler ต้องถูกเรียก 1 ครั้ง แต่ถูกเรียก %d ครั้ง", called.Load())
	}
}
