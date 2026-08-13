package types

import "fmt"

import "strings"

// textKind 多行文本。
type textKind struct{}

func (textKind) Name() string { return KindText }
func (textKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (textKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (textKind) Class() Class   { return ClassField }
func (textKind) Editor() Widget { return WidgetTextarea }

func (textKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
