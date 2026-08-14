package core

import (
	"fmt"

	"github.com/kran/gcm/types"
)

// ── 代数原语 ─────────────────────────────────────────
//
// 图原语不跨类型: 出边字段属于起点节点的类型 — 调用方显式传 typeName
// （拿节点时类型在手, 零查询）; fieldOnType 纯内存归属校验, 拼错立即 fail-loud。
// 入边（InEdges）跨类型（任何类型可声明指向我的边）— 用 refFieldMetaGlobal。

// fieldOnType 归属校验（纯内存）: 字段必须存在于 typeName 类型且是 ref 系。
func (s *Service) fieldOnType(typeName, field string) (types.FieldDef, bool, error) {
	td, ok := s.types.Type(typeName)
	if !ok {
		return types.FieldDef{}, false, fmt.Errorf("core: type %q not defined", typeName)
	}
	f, ok := types.FieldByName(td, field)
	if !ok {
		return types.FieldDef{}, false, fmt.Errorf("core: field %q not on type %q", field, typeName)
	}
	if !s.types.IsRefKind(f.Kind) {
		return types.FieldDef{}, false, fmt.Errorf("core: field %q is not a ref kind", f.Name)
	}
	return f, f.Symmetric, nil
}

// Traverse 沿 field 出边递归（向上: 祖先链）。maxHops 上限防环（有界原则）。
// 返回可达节点 id（含多跳, 不含起点）。typeName = 起点节点类型。
func (s *Service) Traverse(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT to_node, 1 FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// Ancestors 祖先链: 沿 field 出边（父方向）从 start 向上到根。
// 返回根→叶（链首 = 根, 链尾 = 最近父）, 不含 start; 按深度排序
// （Traverse 是 ORDER BY id — 层级乱序不可用于链; 这里是层级语义）。
// 返回 *Node 列表（批量取节点并按链序重排）— 任意级父分类一步拿到。
func (s *Service) Ancestors(typeName string, start int64, field string, maxHops int) ([]*Node, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	ids, err := s.walk(`
		WITH RECURSIVE anc(id, depth) AS (
			SELECT to_node, 1 FROM edges WHERE field = #{1} AND from_node = #{2}
			UNION ALL
			SELECT e.to_node, a.depth + 1 FROM edges e JOIN anc a ON e.from_node = a.id
			WHERE e.field = #{1} AND a.depth < #{3}
		)
		SELECT id FROM anc ORDER BY depth DESC`, field, start, maxHops)
	if err != nil {
		return nil, err
	}
	rows, err := s.nodesByIDs(ids)
	if err != nil {
		return nil, err
	}
	// 按链序重排（IN 查询顺序不定）
	byID := make(map[int64]*Node, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	out := make([]*Node, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// Subtree 沿 field 入边递归（向下: 子树/后代）。maxHops 上限防环。
// 返回全部后代节点 id（不含起点）。
func (s *Service) Subtree(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT from_node, 1 FROM edges WHERE field = #{1} AND to_node = #{2}
			UNION ALL
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// EquivalenceClass 等价类展开: 沿 field 出边+入边双向递归
// （等价关系无方向 — 即使只存单向边, 类内全部成员可达）。
// 返回含起点在内的整个等价类。maxHops 上限（等价类应小, 防御环）。
func (s *Service) EquivalenceClass(typeName string, start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.fieldOnType(typeName, field); err != nil {
		return nil, err
	}
	return s.walk(`
		WITH RECURSIVE walk(id, depth) AS (
			SELECT #{2}, 0
			UNION
			SELECT e.to_node, w.depth + 1 FROM edges e JOIN walk w ON e.from_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
			UNION
			SELECT e.from_node, w.depth + 1 FROM edges e JOIN walk w ON e.to_node = w.id
			WHERE e.field = #{1} AND w.depth < #{3}
		)
		SELECT DISTINCT id FROM walk ORDER BY id`, field, start, maxHops)
}

// walk 执行递归 CTE, 返回 id 列表。
func (s *Service) walk(cte string, args ...any) ([]int64, error) {
	var ids []int64
	q := s.db.Add(cte, args...)
	if err := q.List(&ids); err != nil {
		return nil, fmt.Errorf("core: traverse: %w", err)
	}
	return ids, nil
}
