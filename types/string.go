package types

import "fmt"

import "strings"

// stringKind 单行字符串。
type stringKind struct{}

func (stringKind) Name() string { return KindString }
func (stringKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (stringKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (stringKind) Class() Class   { return ClassField }
func (stringKind) Editor() Widget { return WidgetInput }

func (stringKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
