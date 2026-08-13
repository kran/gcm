package types

import "fmt"

// boolKind 布尔。
type boolKind struct{}

func (boolKind) Name() string { return KindBool }
func (boolKind) Validate(v any) error {
	if _, ok := v.(bool); !ok {
		return fmt.Errorf("expects bool, got %T", v)
	}
	return nil
}
func (boolKind) IsEmpty(v any) bool { _, ok := v.(bool); return !ok }

func (boolKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return rejectRefAttrs(typeName, f)
}
func (boolKind) Class() Class   { return ClassField }
func (boolKind) Editor() string { return "bool" }
