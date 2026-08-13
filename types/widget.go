package types

// Widget 编辑控件原语（Kind.Editor() 的契约 — 有限枚举, 非自由字符串）。
//
// 前端 FieldRenderer 是"原语渲染器": 内置实现这组原语, 未知原语尝试从
// 站点挂载的路由动态加载组件（/admin/ui-extras/{widget}.vue, cmx 面板模式）。
// 新 kind 复用现有原语 → 前端零改动（一处一语言）。
//
// 原语常量（WidgetInput/WidgetTextarea/...）定义在各自 kind 的文件里
// （自包含: 加新 kind 只动一个文件）; array/object 的原语 = 结构语法名。
type Widget string
