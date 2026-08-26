package graph

import (
	"github.com/vektah/gqlparser/v2/ast"
)

// SelectionDepth หาความลึกสูงสุดของ operation
//
// นับ selection ชั้นบนสุดเป็น 1 เช่น `{ viewer { pets { id } } }` ได้ 3
//
// ต้องเดินตาม fragment spread ด้วย ไม่งั้นกันไม่ได้จริง เพราะเขียน query ตื้นๆ
// ที่ spread fragment ซึ่งข้างในซ้อนลึกแค่ไหนก็ได้:
//
//	query Shallow { viewer { ...Deep } }
//	fragment Deep on Viewer { pets { caregivers { user { ... } } } }
//
// inline fragment (`... on Type`) ไม่นับเป็นชั้นเพิ่ม เพราะไม่ได้ทำให้ resolver
// ทำงานลึกขึ้น เป็นแค่การเลือก type
func SelectionDepth(op *ast.OperationDefinition) int {
	if op == nil {
		return 0
	}
	return selectionSetDepth(op.SelectionSet, map[string]bool{})
}

func selectionSetDepth(set ast.SelectionSet, visiting map[string]bool) int {
	deepest := 0
	for _, sel := range set {
		var d int
		switch s := sel.(type) {
		case *ast.Field:
			d = 1 + selectionSetDepth(s.SelectionSet, visiting)

		case *ast.InlineFragment:
			d = selectionSetDepth(s.SelectionSet, visiting)

		case *ast.FragmentSpread:
			if s.Definition == nil {
				continue
			}
			// กัน fragment ที่อ้างวนกัน ไม่ให้เดินไม่รู้จบ
			//
			// ตัว validator ปฏิเสธ fragment cycle อยู่แล้ว แต่โค้ดนี้ถูกเรียก
			// จาก middleware ที่รันหลัง validate ถ้าวันหนึ่งย้ายไปเรียกก่อนหน้านั้น
			// จะได้ไม่กลายเป็นช่องทำให้ server ค้างแทนที่จะกัน
			if visiting[s.Name] {
				continue
			}
			visiting[s.Name] = true
			d = selectionSetDepth(s.Definition.SelectionSet, visiting)
			delete(visiting, s.Name)
		}

		if d > deepest {
			deepest = d
		}
	}
	return deepest
}
