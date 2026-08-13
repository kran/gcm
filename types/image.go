package types

import "fmt"

import "strings"

// imageKind 图片 URL/路径。
type imageKind struct{}

func (imageKind) Name() string { return KindImage }
func (imageKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (imageKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (imageKind) Class() Class   { return ClassField }
func (imageKind) Editor() string { return "upload-image" }

func (imageKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
