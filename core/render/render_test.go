package render

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kran/dba"
	"github.com/kran/gcm/core"
	"github.com/kran/gcm/migrations"
	"github.com/kran/gcm/types"
)

const testTypes = `
types:
  article:
    url: /article/{slug}
    fields:
      - { name: body, kind: richtext }
      - { name: authors, kind: "ref[]", to: person, inverse: articles }
  person:
    fields:
      - { name: name, kind: string, required: true }
      - { name: articles, kind: "ref[]", to: article, inverse: authors }
`

func testSvc(t *testing.T) (*core.Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := dba.Open("sqlite", filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrations.Up(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ts := types.New()
	if err := ts.Load([]byte(testTypes)); err != nil {
		t.Fatal(err)
	}
	return core.New(db, ts), dir
}

// 级联: node--article.html 存在则用它, 否则 node.html。
func TestRenderCascade(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)

	// 只有 node.html
	os.WriteFile(filepath.Join(tplDir, "node.html"),
		[]byte(`<h1>{{ .Msg }}</h1>`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	if err := e.Render(&sb, Candidates(&core.Node{Type: "article"}),
		map[string]any{"Msg": "T"}); err != nil {
		t.Fatal(err)
	}
	if sb.String() != "<h1>T</h1>" {
		t.Fatalf("got %q", sb.String())
	}
}

// 注入函数: 模板调用 outRefs/inRefs/subtree + 数据。
func TestInjectedFuncs(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)

	// 数据: 专家 + 文章 + 分类树
	p1, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "张三"}})
	p2, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "李四"}})
	artID, _ := svc.Create(&core.Node{Type: "article", Slug: "hello",
		Fields: core.Fields{"body": "<p>hi</p>", "authors": []any{p1, p2}}})

	// 模板: 节点 + outRefs + inRefs + 工具函数
	os.WriteFile(filepath.Join(tplDir, "node--article.html"), []byte(`
{{ $n := get .ID }}
<h1>{{ $n.Slug }}</h1>
{{ safeHTML $n.Fields.body }}
authors:{{ range outRefs .ID "authors" 1 10 }}[{{ .Fields.name }}]{{ end }}
refd-by:{{ range inRefs .P1 "authors" 1 10 }}[{{ .Slug }}]{{ end }}
`), 0644)

	e := New(tplDir, svc)
	var sb strings.Builder
	err := e.Render(&sb, Candidates(&core.Node{Type: "article", ID: artID}),
		map[string]any{"ID": artID, "P1": p1})
	if err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "<h1>hello</h1>") {
		t.Fatalf("slug missing: %s", out)
	}
	if !strings.Contains(out, "[张三][李四]") {
		t.Fatalf("authors missing: %s", out)
	}
	if !strings.Contains(out, "[hello]") {
		t.Fatalf("inRefs missing: %s", out)
	}
}

// 查询错误 fail-loud: 未知字段 outRefs → 渲染失败。
func TestInjectedFailLoud(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	os.WriteFile(filepath.Join(tplDir, "node.html"),
		[]byte(`{{ range outRefs 1 "ghost" 1 10 }}{{ end }}`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	if err := e.Render(&sb, Candidates(&core.Node{Type: "x"}),
		map[string]any{}); err == nil {
		t.Fatal("query error must fail render")
	}
}

// expand 统一模板函数: 列表/单值任意形态输入, 批量展开（查询次数与列表大小无关）。
func TestExpandFunc(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	// 数据: 2 文章各 1 作者
	p1, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "张三"}})
	p2, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "李四"}})
	a1, _ := svc.Create(&core.Node{Type: "article", Slug: "a1", Status: core.StatusPublished, Fields: core.Fields{"body": "x", "authors": []any{p1}}})
	a2, _ := svc.Create(&core.Node{Type: "article", Slug: "a2", Status: core.StatusPublished, Fields: core.Fields{"body": "y", "authors": []any{p2}}})

	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(`
{{ $arts := list "article" 1 1 10 }}
{{ $arts = $arts | expand "authors" }}
{{ range $arts }}{{ .Slug }}:{{ range .Expand.authors }}[{{ .Fields.name }}]{{ end }};{{ end }}
{{ $one := get `+strconv.FormatInt(a1, 10)+` | expand "authors" }}
one={{ range $one.Expand.authors }}{{ .Fields.name }}{{ end }}
`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	if err := e.Render(&sb, []string{"node.html"}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "a1:[张三];a2:[李四];") || !strings.Contains(out, "one=张三") {
		t.Fatalf("expand: %s", out)
	}
	_ = a2
}

// filterList 模板函数: 模板一行调用, 占位符绑定, 编译缓存。
func TestFilterListFunc(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	// 数据: 3 文章（2 已发布, 1 草稿）; 张三发布 2 篇
	p1, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "张三"}})
	p2, _ := svc.Create(&core.Node{Type: "person", Fields: core.Fields{"name": "李四"}})
	svc.Create(&core.Node{Type: "article", Slug: "a1", Status: core.StatusPublished, Fields: core.Fields{"body": "x", "authors": []any{p1}}})
	svc.Create(&core.Node{Type: "article", Slug: "a2", Status: core.StatusPublished, Fields: core.Fields{"body": "y", "authors": []any{p2}}})
	svc.Create(&core.Node{Type: "article", Slug: "a3", Status: core.StatusDraft, Fields: core.Fields{"body": "z", "authors": []any{p2}}})

	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(`
{{ $pub := filterList "article" "status = 1" nil 1 10 }}
pub={{ range $pub }}{{ .Slug }} {{ end }}
{{ $z := filterList "article" "status = 1 && authors ~ {:a}" (dict "a" 1) 1 10 }}
zhang={{ range $z }}{{ .Slug }} {{ end }}
`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	err := e.Render(&sb, []string{"node.html"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, "pub=a2 a1") || !strings.Contains(out, "zhang=a1 ") {
		t.Fatalf("filterList: %s", out)
	}
	// 缓存命中: 同表达式不重复编译（内部不可见, 但保证不 panic/结果一致）
	var sb2 strings.Builder
	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(`{{ $z := filterList "article" "status = 1 && authors ~ {:a}" (dict "a" 1) 1 10 }}{{ range $z }}{{ .Slug }}{{ end }}`), 0644)
	if err := e.Render(&sb2, []string{"node.html"}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if sb2.String() != "a1" {
		t.Fatalf("filterList cached: %s", sb2.String())
	}
	_ = p2
}

// filterList 语法错误 → 渲染失败（fail-loud, 错误信息含 filter 原因）。
func TestFilterListFailLoud(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(`{{ $bad := filterList "article" "authors ~" nil 1 10 }}`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	err := e.Render(&sb, []string{"node.html"}, map[string]any{})
	if err == nil {
		t.Fatal("bad filter must fail render (fail-loud)")
	}
	if !strings.Contains(err.Error(), "filter:") {
		t.Fatalf("error must surface filter cause: %v", err)
	}
}

// tpl 机制（并入 render 包）: 片段组合 / 兜底 / safeHTML / plainText / sprig。
func TestTplMechanisms(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(filepath.Join(tplDir, "partials"), 0755)
	os.WriteFile(filepath.Join(tplDir, "partials/head.html"),
		[]byte(`<title>{{ .Title }}</title>`), 0644)
	os.WriteFile(filepath.Join(tplDir, "partials/footer.html"),
		[]byte(`<footer>{{ .Year }}</footer>`), 0644)
	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(
		`{{ partial "partials/head.html" (dict "Title" "页") }}`+
			`{{ partialOr "partials/missing.html" "partials/footer.html" (dict "Year" 2026) }}`+
			`{{ safeHTML "<b>粗</b>" }}`+
			`[{{ plainText "<p>段一</p><p>段二</p>" }}]`+
			`{{ default "d" "" }}`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	if err := e.Render(&sb, []string{"node.html"}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{"<title>页</title>", "<footer>2026</footer>", "<b>粗</b>", "段一", "段二", "d"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tpl mechanisms: missing %q in %s", want, out)
		}
	}
}

// partialOr 兜底只对"片段缺失"; 片段解析/执行错误响亮上抛。
func TestPartialOrErrorNotSwallowed(t *testing.T) {
	svc, dir := testSvc(t)
	tplDir := filepath.Join(dir, "templates")
	os.MkdirAll(tplDir, 0755)
	os.WriteFile(filepath.Join(tplDir, "bad.html"), []byte(`{{ index .Missing 5 }}`), 0644)
	os.WriteFile(filepath.Join(tplDir, "node.html"), []byte(
		`{{ partialOr "bad.html" "fallback.html" . }}`), 0644)
	e := New(tplDir, svc)
	var sb strings.Builder
	if err := e.Render(&sb, []string{"node.html"}, map[string]any{}); err == nil {
		t.Fatal("partial execute error must not fall back silently")
	}
}
