package core

import (
	"strings"
	"testing"
)

// FuzzFilterParse 模糊测试: 任意输入不 panic、不死循环。
// 合法则继续 Build（也不 panic）; 非法拒绝是预期（不崩溃）。
func FuzzFilterParse(f *testing.F) {
	seeds := []string{
		`status = 1`,
		`authors ~ {:x}`,
		`$.title like "ai"`,
		`<-authors`,
		`<-authors ~ 5`,
		`authors.$.level = "senior"`,
		`(categories ~ {:c} || categories ~ 2) && !status = 0`,
		`((((status = 1))))`,
		`"a\\\"b" = $.title`,
		`status == 1`,
		`{:}`,
		``,
		`@#$%`,
		`status = 1 && || ! ( ) ~`,
		strings.Repeat(`(`, 50) + `status = 1` + strings.Repeat(`)`, 50),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		s := newFilterSvc(t)
		cf, err := s.CompileFilter(input)
		if err != nil {
			return // 非法拒绝是预期
		}
		// 编译成功 → Build 不应 panic（占位符缺绑定返回 error 可接受）
		_, _, _ = s.BuildFilter(cf, "article", map[string]any{"x": int64(1)})
	})
}
