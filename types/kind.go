package types

// Kind 字段类型契约（值语义）。
//
// 每个内置 kind 一个文件、一个独立实现（最笨但可迁移: 各自独立演进,
// 将来给 image 加 URL 校验只动 image.go, 不碰其他）。
// Kind 只管"值语义": 校验 / 空判断 / 存储形态。
// 代数/To 在 FieldDef（类型定义层, 见 types.go）。
type Kind interface {
	Name() string
	// Validate 值校验。字段路径（typeName.fieldName）由调用方包装。
	Validate(v any) error
	// IsEmpty required 检查: 值是否为空（空串/空数组/非法 id）。
	IsEmpty(v any) bool
	// Class 分类: 值存哪、是什么形态 — kind 自己的声明, 引擎零推断。
	Class() Class
	// Editor 编辑控件原语（Widget 有限枚举）: 新 kind 复用现有原语 → 前端
	// 零改动; 全新原语 → 站点挂载组件路由（/admin/ui-extras/{widget}.vue）。
	Editor() Widget
	// ValidateField 字段定义校验: 本 kind 对 FieldDef 的约束。
	// 通用校验（类型名/字段名/重复/kind 存在）由容器做;
	// 这里只做 kind 特有规则（标量禁代数 / ref 要 to / 代数互斥）。
	// defs 是同一批待校验的类型定义（容器校验阶段 defs 尚未落容器）。
	ValidateField(t *Types, typeName string, f FieldDef, defs map[string]TypeDef) error
}

// Class 分类: 值存哪、是什么形态。
// 三态枚举覆盖全部合法组合（inline 恒单值; ref 单/多两态）,
// 不存在"inline + 数组"这类非法组合。
type Class int

const (
	ClassField   Class = iota // 标量: 值存 fields JSON
	ClassRef                  // 单引用: 值 → 1 条 edge
	ClassRefList              // 多引用: 值 → N 条 edge
)
