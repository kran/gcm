// API: 记录查询端点（公开只读）。
//
//	GET /api/nodes/{type}?filter=<lisp>&sort=&page=&size=&expand=
//
// 参数:
//
//	{type} 路径参数 — 类型过滤（API 层合成 (= type "x") 进 filter）
//	filter Lisp filter 表达式（(and (= status 1) (in ->categories (subtree "root")))）
//	sort   列白名单 + asc|desc（"sort, id DESC" — 防 ORDER BY 注入）
//	page   页号（默认 1）
//	size   每页数（默认 20）
//	expand 逗号分隔展开路径（"authors, categories" / "categories.parent"）
//
// 返回: {"items": [Node...], "total": N} — Node 完整序列化
// （含 expand 容器）; filter 编译错误/非法参数 → 400（fail-loud）。
package web

import (
	"net/http"
	"strings"

	"github.com/kran/gcm/core"
)

// apiSortCols ORDER BY 白名单（nodes 列; JSON 字段排序不在 API 边界内 —
// 需要时 Go 层 Q 直接拼）。
var apiSortCols = map[string]bool{
	"id": true, "type": true, "title": true, "slug": true,
	"status": true, "sort": true, "created_at": true, "updated_at": true,
}

// apiSortValid 校验 sort 参数: 列白名单 + 可选 asc/desc, 逗号分隔多列。
func apiSortValid(sort string) bool {
	sort = strings.TrimSpace(sort)
	if sort == "" {
		return true
	}
	for _, part := range strings.Split(sort, ",") {
		cols := strings.Fields(strings.TrimSpace(part))
		if len(cols) < 1 || len(cols) > 2 {
			return false
		}
		if !apiSortCols[cols[0]] {
			return false
		}
		if len(cols) == 2 {
			d := strings.ToLower(cols[1])
			if d != "asc" && d != "desc" {
				return false
			}
		}
	}
	return true
}

// apiNodes /api/nodes/{type} — 记录查询（Q 结构化查询直通）。
func (s *Site) apiNodes(ctx *CmsCtx) {
	typ := strings.TrimSpace(ctx.PathValue("type"))
	if typ == "" {
		ctx.String(http.StatusBadRequest, "api: type is required")
		return
	}
	if _, ok := s.svc.Types().Type(typ); !ok {
		ctx.String(http.StatusBadRequest, "api: type "+typ+" not defined")
		return
	}
	filter := strings.TrimSpace(ctx.Query("filter"))
	sort := strings.TrimSpace(ctx.Query("sort"))
	if !apiSortValid(sort) {
		ctx.String(http.StatusBadRequest, "api: invalid sort (column whitelist + asc/desc)")
		return
	}
	page := ctx.QueryInt("page", 1)
	size := ctx.QueryInt("size", 20)
	expand := strings.TrimSpace(ctx.Query("expand"))

	// 类型过滤由 API 层构建（{type} 路径参数 → (= type {:typ}) 参数化）
	f := filter
	if f != "" {
		f = `(and (= type {:typ}) ` + f + `)`
	} else {
		f = `(= type {:typ})`
	}
	q := core.ListQuery{Filter: f, Sort: sort, Expand: expand, Page: page, Size: size}
	list, total, err := s.svc.Q(q, map[string]any{"typ": typ})
	if err != nil {
		// filter 编译错误（语法/未知字段/未知函数）— fail-loud 400
		ctx.String(http.StatusBadRequest, "api: "+err.Error())
		return
	}
	items := make([]core.Node, len(list))
	for i := range list {
		items[i] = list[i]
	}
	_ = ctx.Json(http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}
