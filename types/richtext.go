package types

import "fmt"

import "strings"

// richtextKind 富文本 HTML。
type richtextKind struct{}

func (richtextKind) Name() string { return KindRichtext }
func (richtextKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (richtextKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (richtextKind) Class() Class   { return ClassField }
func (richtextKind) Editor() string { return "richtext" }

func (richtextKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
