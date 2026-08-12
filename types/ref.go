package types

// refKind 单引用: 值是一个整数节点 id。
type refKind struct{}

func (refKind) Name() string { return KindRef }
func (refKind) Validate(v any) error {
	if _, err := ToID(v); err != nil {
		return err
	}
	return nil
}
func (refKind) IsEmpty(v any) bool { _, err := ToID(v); return err != nil }

func (refKind) ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error {
	return validateRefField(t, typeName, f, defs)
}
func (refKind) Class() Class { return ClassRef }
