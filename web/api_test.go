package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kran/gcm/core"
)

// /api/nodes: Lisp filter + sort + 分页 + expand + 错误路径。
func TestAPINodes(t *testing.T) {
	s, svc := newSite(t)
	MountAPI(s)
	zhang, _ := svc.CreateNode(&core.Node{Type: "person", Slug: "zhang", Fields: core.Fields{"name": "张三"}})
	wang, _ := svc.CreateNode(&core.Node{Type: "person", Slug: "wang", Fields: core.Fields{"name": "王五"}})
	for i := 0; i < 5; i++ {
		authors := []any{zhang}
		if i%2 == 1 {
			authors = []any{wang}
		}
		if _, err := svc.CreateNode(&core.Node{Type: "article", Status: core.StatusPublished,
			Fields: core.Fields{"body": "x", "authors": authors}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = zhang
	_ = wang

	// 1. 无 type 路径 → 404（路由不匹配）; /api/nodes/ 空 type 也不匹配
	rec := get(t, s, "/api/nodes")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no type: %d", rec.Code)
	}
	// 2. 未知类型
	rec = get(t, s, "/api/nodes/ghost")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("ghost type: %d", rec.Code)
	}
	// 3. 全部文章
	rec = get(t, s, "/api/nodes/article")
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []core.Node `json:"items"`
		Total int64       `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 5 || len(out.Items) != 5 {
		t.Fatalf("total=%d items=%d", out.Total, len(out.Items))
	}
	// 4. Lisp filter: 作者是张三
	rec = get(t, s, "/api/nodes/article?filter="+urlq(`(edge ->authors (= $name "张三"))`))
	if rec.Code != 200 {
		t.Fatalf("filter: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 3 {
		t.Fatalf("filter total=%d", out.Total)
	}
	// 5. filter 编译错误 → 400
	rec = get(t, s, "/api/nodes/article?filter="+urlq(`(bogus-fn 1)`))
	if rec.Code != 400 {
		t.Fatalf("bad filter: %d", rec.Code)
	}
	// 6. 分页
	rec = get(t, s, "/api/nodes/article?page=2&size=2")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Total != 5 || len(out.Items) != 2 {
		t.Fatalf("page: total=%d items=%d", out.Total, len(out.Items))
	}
	// 7. sort 白名单: 非法 → 400
	rec = get(t, s, "/api/nodes/article?sort=id+DROP+TABLE")
	if rec.Code != 400 {
		t.Fatalf("sort injection: %d", rec.Code)
	}
	rec = get(t, s, "/api/nodes/article?sort=id+DESC,title+ASC")
	if rec.Code != 200 {
		t.Fatalf("sort valid: %d", rec.Code)
	}
	// 8. expand: 文章带 authors
	rec = get(t, s, "/api/nodes/article?size=1&expand=authors")
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	auths, ok := out.Items[0].Expand["authors"].([]any)
	if !ok || len(auths) == 0 {
		t.Fatalf("expand authors: %#v", out.Items[0].Expand)
	}
}

func urlq(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, " ", "%20"), `"`, "%22")
}
