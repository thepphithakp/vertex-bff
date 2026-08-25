//go:build tools

// ตรึง gqlgen ไว้เป็น dependency ของโปรเจกต์ ไม่ให้ go mod tidy ตัดทิ้ง
// เพราะใช้ตอน generate เท่านั้น ไม่ได้ถูก import จากโค้ดที่ build จริง
package main

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/graphql/introspection"
)
