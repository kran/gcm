package types

import "fmt"

// numberKind 数值（int/uint 家族 + float64）。
type numberKind struct{}

func (numberKind) Name() string { return KindNumber }
func (numberKind) Validate(v any) error {
	if !isNumber(v) {
		return fmt.Errorf("expects number, got %T", v)
	}
	return nil
}
func (numberKind) IsEmpty(v any) bool { return !isNumber(v) }

func (numberKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (numberKind) Class() Class { return ClassField }
