package types

import (
	"encoding/json"
	"fmt"
)

// isNumber 接受 JSON 解码（float64）与程序直构（int/uint 全家族）两种来源。
func isNumber(v any) bool {
	switch v.(type) {
	case float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// rejectRefAttrs 标量 kind 的字段约束: 不得声明引用相关属性。
func rejectRefAttrs(typeName string, f FieldDef) error {
	if f.To != "" || f.Symmetric || f.Transitive || f.Equivalence {
		return fmt.Errorf("types: %q.%s: kind %s must not declare to/algebra", typeName, f.Name, f.Kind)
	}
	return nil
}

// validateRefField ref 系 kind 共享的字段约束（宪法）:
// to 必填且存在、代数互斥。边双向无需 reverse 声明（InRefs 引擎原语）。
func validateRefField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	if f.To == "" {
		return fmt.Errorf("types: %q.%s: ref kind requires to", typeName, f.Name)
	}
	if _, ok := defs[f.To]; !ok {
		return fmt.Errorf("types: %q.%s: to %q not defined", typeName, f.Name, f.To)
	}
	// 代数互斥: 对称/传递/等价三选一
	n := 0
	for _, on := range []bool{f.Symmetric, f.Transitive, f.Equivalence} {
		if on {
			n++
		}
	}
	if n > 1 {
		return fmt.Errorf("types: %q.%s: symmetric/transitive/equivalence are mutually exclusive", typeName, f.Name)
	}
	return nil
}

// ToID 数值 → int64（JSON 解码后是 float64; 非整数拒绝）。
// 引用值解析的唯一入口（core 引擎与 kind 实现共用）。
func ToID(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("expects integer node id, got %v", n)
		}
		return int64(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("expects integer node id, got %v", n)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("expects node id (int64), got %T", v)
	}
}
