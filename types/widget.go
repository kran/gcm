package types

// Widget 编辑控件原语（Kind.Editor() 的契约 — 有限枚举, 非自由字符串）。
//
// 前端 FieldRenderer 是"原语渲染器": 内置实现这组原语, 未知原语尝试从
// 站点挂载的路由动态加载组件（/admin/ui-extras/{widget}.vue, cmx 面板模式）。
// 新 kind 复用现有原语 → 前端零改动（一处一语言）。
type Widget string

const (
	WidgetInput        Widget = "text"         // 单行输入
	WidgetTextarea     Widget = "textarea"     // 多行文本
	WidgetRichtext     Widget = "richtext"     // 富文本
	WidgetNumber       Widget = "number"       // 数字输入
	WidgetSwitch       Widget = "bool"         // 开关
	WidgetUploadImage  Widget = "upload-image" // 图片上传
	WidgetUploadFile   Widget = "upload-file"  // 文件上传
	WidgetEntityPicker Widget = "ref"          // 单引用选择器
	WidgetEntityList   Widget = "ref[]"        // 多引用选择器
	WidgetArray        Widget = "array"        // 数组容器（结构）
	WidgetObject       Widget = "object"       // 对象容器（结构）
)
