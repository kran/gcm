package types

import "fmt"

import "strings"

// fileKind 文件 URL/路径（上传产物）。
type fileKind struct{}

func (fileKind) Name() string { return KindFile }
func (fileKind) Validate(v any) error {
	if _, ok := v.(string); !ok {
		return fmt.Errorf("expects string, got %T", v)
	}
	return nil
}
func (fileKind) IsEmpty(v any) bool {
	s, ok := v.(string)
	return !ok || strings.TrimSpace(s) == ""
}
func (fileKind) Class() Class   { return ClassField }
func (fileKind) Editor() string { return "upload-file" }

func (fileKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
