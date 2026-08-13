package core

import (
	"testing"

	"github.com/kran/gcm/types"
)

// 配置类型注册表: 每项 = 完整 FieldDef（标量 + array<object> 复合）;
// 校验复用 types.ValidateValue（元素/子字段递归）。
func TestSettings(t *testing.T) {
	s := newFilterSvc(t)
	s.SettingTypes(
		SettingType{Key: "footer", Field: types.FieldDef{Kind: "richtext"}, Note: "页脚内容"},
		SettingType{Key: "slogan", Field: types.FieldDef{Kind: "string"}, Note: "标语"},
		SettingType{Key: "nav_links", Field: types.FieldDef{
			Kind: "array", Item: &types.FieldDef{
				Kind: "object", Fields: []types.FieldDef{
					{Name: "label", Kind: "string", Required: true},
					{Name: "url", Kind: "string"},
				},
			},
		}, Note: "导航链接"},
	)
	// 合法写入
	if err := s.SetSetting("footer", "<p>版权</p>"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("nav_links", []any{
		map[string]any{"label": "首页", "url": "/"},
		map[string]any{"label": "关于"},
	}); err != nil {
		t.Fatal(err)
	}
	// 批量取
	got, err := s.GetSettings([]string{"footer", "nav_links", "missing"})
	if err != nil || len(got) != 2 {
		t.Fatalf("getmany: %d %v", len(got), err)
	}
	// 校验: 复合元素/子字段递归（label 必填）
	if err := s.SetSetting("nav_links", []any{map[string]any{"url": "/"}}); err == nil {
		t.Fatal("required sub-field must fail")
	}
	if err := s.SetSetting("nav_links", []any{map[string]any{"label": 5, "url": "/"}}); err == nil {
		t.Fatal("sub-field type must fail")
	}
	// 未注册 key 拒绝
	if err := s.SetSetting("ghost", "x"); err == nil {
		t.Fatal("unregistered key must fail")
	}
	// 单取 + 缺失
	one, _ := s.GetSetting("slogan")
	if one != nil {
		t.Fatal("unset must be nil")
	}
	if miss, _ := s.GetSetting("ghost"); miss != nil {
		t.Fatal("unregistered must be nil")
	}
}
