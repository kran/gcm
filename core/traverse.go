package core

import "fmt"

// ── 代数原语 ─────────────────────────────────────────
//
// symmetric 已在 M3（OutEdges/InEdges 双向展开）; 反向查询无需声明（边双向）。
// 这里补 transitive（可达性）与 equivalence（等价类）。
// 原语是通用能力（任何 ref 字段都能递归）; 代数声明决定查询语言
// （M5）里是否允许闭包/等价展开 — 原语本身不检查代数。

// Traverse 沿 field 出边递归（向上: 祖先链）。maxHops 上限防环（有界原则）。
// 返回可达节点 id（含多跳, 不含起点）。
func (s *Service) Traverse(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
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
func (s *Service) Ancestors(start int64, field string, maxHops int) ([]*Node, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
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
func (s *Service) Subtree(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
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
func (s *Service) EquivalenceClass(start int64, field string, maxHops int) ([]int64, error) {
	if _, _, err := s.refFieldMeta(start, field); err != nil {
		return nil, err
	}
	if maxHops < 1 {
		maxHops = 1
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
