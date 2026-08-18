package render

// 模板引擎机制（原 core/tpl 包, 并入 render）:
//   - 级联解析: Render 按候选序取第一个存在的文件
//   - 片段:     partial / partialOr（带兜底）— 页面结构由模板自行引入
//   - 函数注入: 每引擎独立 FuncMap — 查询函数定义在 Go 层（queryFuncs）,
//     模板执行时合并 sprig（安全子集）+ 内置函数
// 无缓存: 每次渲染读文件+解析, 热重载; 失败响亮。

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/sprig/v3"
	"jaytaylor.com/html2text"
)

// execute 单个模板文件独立解析执行 (无隐式布局 — 页面结构由模板自行
// 经 partial 引入)。
func (e *Engine) execute(w io.Writer, name, full string, data any) error {
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap()).ParseFiles(full)
	if err != nil {
		return fmt.Errorf("render: parse %s: %w", name, err)
	}
	return tpl.Execute(w, data)
}

// Partial 渲染片段模板 (独立解析执行; 不参与布局)。
// 无缓存, 每次渲染读当前文件 — 片段也热重载。
func (e *Engine) Partial(name string, data any) (template.HTML, error) {
	clean := filepath.Clean(name)
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("render: partial %q escapes templates root", name)
	}
	full := filepath.Join(e.root, clean)
	if _, err := os.Stat(full); err != nil {
		return "", errPartialNotFound
	}
	tpl, err := template.New(filepath.Base(full)).Funcs(e.funcMap()).ParseFiles(full)
	if err != nil {
		return "", fmt.Errorf("render: parse partial %s: %w", name, err)
	}
	var sb strings.Builder
	if err := tpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render: partial %q: %w", name, err)
	}
	return template.HTML(sb.String()), nil
}

// errPartialNotFound 片段缺失哨兵: partialOr 据此回落兜底。
var errPartialNotFound = errors.New("render: partial not found")

// funcMap 内置函数 + sprig (安全子集) + 查询/自定义函数。
// sprig: HermeticHtmlFuncMap — 无 env/expandenv 等环境访问的安全子集
// (dict/default/trunc 等常用模板工具函数)。
// safeHTML: 受信富文本原样输出 (匿名提交内容禁用, XSS)。
func (e *Engine) funcMap() template.FuncMap {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m := template.FuncMap{}
	for k, v := range sprig.HermeticHtmlFuncMap() {
		m[k] = v
	}
	builtins := template.FuncMap{
		"safeHTML": func(v any) template.HTML { return template.HTML(fmt.Sprint(v)) },
		// img 图片裁剪 URL: 本地 /uploads/ 才拼 ?w=&h=&mode=&fmt=（CDN/外部 URL 原样返回）。
		// 单边 0 = 按比例; mode: cover(默认)/fit/crop; fmt: jpg/png（照片类 jpg 降体积）。
		"img": func(url string, w, h int, mode string, fmtArgs ...string) string {
			if !strings.HasPrefix(url, "/uploads/") && !strings.HasPrefix(url, "/static/") {
				return url
			}
			var b strings.Builder
			b.WriteString(url)
			b.WriteString("?")
			if w > 0 {
				b.WriteString(fmt.Sprintf("w=%d", w))
			}
			if h > 0 {
				if w > 0 {
					b.WriteString("&")
				}
				b.WriteString(fmt.Sprintf("h=%d", h))
			}
			if mode != "" {
				b.WriteString("&mode=" + mode)
			}
			if len(fmtArgs) > 0 && fmtArgs[0] != "" {
				b.WriteString("&fmt=" + fmtArgs[0])
			}
			return b.String()
		},
		"partial": func(name string, data any) (template.HTML, error) {
			return e.Partial(name, data)
		},
		"partialOr": func(name, fallback string, data any) (template.HTML, error) {
			out, err := e.Partial(name, data)
			if err != nil {
				if errors.Is(err, errPartialNotFound) {
					return e.Partial(fallback, data)
				}
				return "", err // 解析/执行错误响亮上抛, 不吞进兜底
			}
			return out, nil
		},
		// HTML → 纯文本 (列表/首页缩略): 剥标签 + 块元素转换行
		"plainText": func(html string) string {
			if html == "" {
				return ""
			}
			text, err := html2text.FromString(html)
			if err != nil {
				return strings.TrimSpace(regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " "))
			}
			return strings.TrimSpace(text)
		},
	}
	for k, v := range builtins { // 内置覆盖 sprig 同名 (如有)
		m[k] = v
	}
	for k, v := range e.funcs {
		m[k] = v
	}
	return m
}
