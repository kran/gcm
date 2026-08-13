package core

import (
	"testing"
)

// settings: 批量取 / 单取 / upsert 校验（kind 未知拒绝 + 值校验复用 types）。
func TestSettings(t *testing.T) {
	s := newFilterSvc(t)
	// Set: 合法
	if err := s.SetSetting(Setting{Key: "footer", Group: "site", Kind: "richtext", Note: "页脚内容", Value: "<p>版权</p>"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(Setting{Key: "slogan", Group: "site", Kind: "string", Note: "标语", Value: "实体关系 CMS"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(Setting{Key: "max_items", Group: "list", Kind: "number", Note: "列表条数", Value: float64(10)}); err != nil {
		t.Fatal(err)
	}
	// 批量取: 3 键 + 1 缺失 → 3 条
	got, err := s.GetSettings([]string{"footer", "slogan", "max_items", "missing"})
	if err != nil || len(got) != 3 {
		t.Fatalf("getmany: %d %v", len(got), err)
	}
	if got["footer"].Value != "<p>版权</p>" || got["max_items"].Value != float64(10) {
		t.Fatalf("values: %v", got["max_items"].Value)
	}
	// 单取
	one, err := s.GetSetting("slogan")
	if err != nil || one == nil || one.Value != "实体关系 CMS" {
		t.Fatalf("get: %v %v", one, err)
	}
	if miss, _ := s.GetSetting("ghost"); miss != nil {
		t.Fatal("missing must be nil")
	}
	// 分组列表
	list, err := s.ListSettings("site")
	if err != nil || len(list) != 2 {
		t.Fatalf("list by group: %d %v", len(list), err)
	}
	// upsert 覆盖
	if err := s.SetSetting(Setting{Key: "slogan", Group: "site", Kind: "string", Note: "新标语", Value: "改"}); err != nil {
		t.Fatal(err)
	}
	if updated, _ := s.GetSetting("slogan"); updated.Value != "改" || updated.Note != "新标语" {
		t.Fatalf("upsert: %v", updated)
	}
	// 校验: 未知 kind 拒绝 / 值类型错拒绝
	if err := s.SetSetting(Setting{Key: "x", Kind: "ghost", Value: "y"}); err == nil {
		t.Fatal("unknown kind must fail")
	}
	if err := s.SetSetting(Setting{Key: "x", Kind: "number", Value: "not-a-number"}); err == nil {
		t.Fatal("value validation must fail")
	}
}

// kind 配置实例: array = array<string> 默认配置（校验元素）; object = 形状检查。
func TestSettingsComposite(t *testing.T) {
	s := newFilterSvc(t)
	// array<string>: 合法 / 非字符串元素拒绝
	if err := s.SetSetting(Setting{Key: "tags", Group: "seo", Kind: "array", Value: []any{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(Setting{Key: "x", Kind: "array", Value: []any{"a", 5}}); err == nil {
		t.Fatal("array element must be string")
	}
	// object: 形状检查（自由 map, 无子定义）
	if err := s.SetSetting(Setting{Key: "meta", Kind: "object", Value: map[string]any{"og": "x"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(Setting{Key: "y", Kind: "object", Value: "not-map"}); err == nil {
		t.Fatal("object must be map")
	}
}
