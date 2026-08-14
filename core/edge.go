package core

import (
	"errors"
	"fmt"
	"time"

	"github.com/kran/dba"
)

// Edge 引用（无身份引用, edges 表的行）。
// 类型系统不可见 — 用户只看到"节点有引用字段"。
type Edge struct {
	ID        int64     `db:"id,omitempty"`
	FromNode  int64     `db:"from_node"`
	Field     string    `db:"field"`
	ToNode    int64     `db:"to_node"`
	Sort      int       `db:"sort"`
	CreatedAt time.Time `db:"created_at"`
}

// ErrEdgeNotFound 目标引用不存在。
var ErrEdgeNotFound = errors.New("core: edge not found")

// AddEdge 公开写入口: 加一条引用。
// 校验（fail-loud）: from 存在、field 属于 from 类型且是引用系、
// to 存在且类型匹配; 重复（UNIQUE）报错。
func (s *Service) AddEdge(from, to int64, field string, sort int) (int64, error) {
	// 先查 from 节点（存在性 + 类型）→ fieldOnType 归属校验
	fromNode, err := s.GetNodeById(from)
	if err != nil {
		return 0, err
	}
	if fromNode == nil {
		return 0, fmt.Errorf("core: addref: from node %d not found", from)
	}
	f, _, err := s.fieldOnType(fromNode.Type, field)
	if err != nil {
		return 0, err
	}
	// to 存在且类型匹配（checkTarget 内部查节点）
	if err := s.checkTarget(s.db, to, f.To); err != nil {
		return 0, fmt.Errorf("core: addref: %w", err)
	}
	var id int64
	err = s.db.Transaction(func(tx *dba.SQL) error {
		res, err := tx.Add(
			`INSERT INTO edges (from_node, field, to_node, sort, created_at)
			 VALUES (#{1}, #{2}, #{3}, #{4}, datetime('now'))`,
			from, field, to, sort).Exec()
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

// RemoveEdge 删一条引用（按 id）。
func (s *Service) RemoveEdge(id int64) error {
	res, err := s.db.Add(
		`DELETE FROM edges WHERE id = #{1}`, id).Exec()
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrEdgeNotFound
	}
	return nil
}

// ── 查询 ─────────────────────────────────────────

// OutEdges 出边列表: from 出发、按 field 过滤、分页。
// symmetric 字段: 双向展开（from=id OR to=id, 存一条查两向）。
func (s *Service) OutEdges(typeName string, from int64, field string, page, size int) ([]Edge, int64, error) {
	_, sym, err := s.fieldOnType(typeName, field)
	if err != nil {
		return nil, 0, err
	}
	if sym {
		return s.edgePage("field = #{1} AND (from_node = #{2} OR to_node = #{2})",
			[]any{field, from}, page, size)
	}
	return s.edgePage("field = #{1} AND from_node = #{2}",
		[]any{field, from}, page, size)
}

// InEdges 入边列表: 指向 to、按 field 过滤、分页（反向查询 — 边双向, 引擎原语直查）。
// symmetric 字段: 双向展开（与 OutEdges 同一集合）。
func (s *Service) InEdges(to int64, field string, page, size int) ([]Edge, int64, error) {
	// 宽松: 不校验字段（拼错 → 空结果; symmetric 双向由 OutEdges 承担）
	return s.edgePage("field = #{1} AND to_node = #{2}",
		[]any{field, to}, page, size)
}

// ── 一致性 ─────────────────────────────────────────

// Merge 合并节点: from 的全部出/入引用改指向 to, 冲突边去重（保留 to 的）,
// 最后删除 from（事务）。
func (s *Service) Merge(from, to int64) error {
	if from == to {
		return errors.New("core: merge: from == to")
	}
	fromNode, err := s.GetNodeById(from)
	if err != nil {
		return err
	}
	if fromNode == nil {
		return ErrNotFound
	}
	toNode, err := s.GetNodeById(to)
	if err != nil {
		return err
	}
	if toNode == nil {
		return ErrNotFound
	}
	return s.db.Transaction(func(tx *dba.SQL) error {
		// 1. 出边冲突去重（from 的边若 to 已有同 field+to_node, 删 from 的）
		if _, err := tx.Add(
			`DELETE FROM edges WHERE from_node = #{1} AND EXISTS (
				SELECT 1 FROM edges e2 WHERE e2.from_node = #{2}
				  AND e2.field = edges.field AND e2.to_node = edges.to_node)`,
			from, to).Exec(); err != nil {
			return err
		}
		// 2. from 剩余出边改指向 to
		if _, err := tx.Add(
			`UPDATE edges SET from_node = #{1} WHERE from_node = #{2}`, to, from).Exec(); err != nil {
			return err
		}
		// 3. 入边冲突去重
		if _, err := tx.Add(
			`DELETE FROM edges WHERE to_node = #{1} AND EXISTS (
				SELECT 1 FROM edges e2 WHERE e2.to_node = #{2}
				  AND e2.field = edges.field AND e2.from_node = edges.from_node)`,
			from, to).Exec(); err != nil {
			return err
		}
		// 4. from 剩余入边改指向 to
		if _, err := tx.Add(
			`UPDATE edges SET to_node = #{1} WHERE to_node = #{2}`, to, from).Exec(); err != nil {
			return err
		}
		// 5. 删 from 节点（引用已清, 防御性再清一次）
		if _, err := tx.Add(
			`DELETE FROM edges WHERE from_node = #{1} OR to_node = #{1}`, from).Exec(); err != nil {
			return err
		}
		affected, err := s.dao.WithTx(tx).Delete("id = #{1}", from)
		if err != nil {
			return err
		}
		if affected == 0 {
			return ErrNotFound
		}
		return nil
	})
}

// ── 工具 ─────────────────────────────────────────

// edgePage 分页查 edges（占位符从 #{n} 递增）。
func (s *Service) edgePage(where string, args []any, page, size int) ([]Edge, int64, error) {
	var total int64
	found, err := s.db.Add(
		`SELECT COUNT(1) FROM edges WHERE `+where, args...).Get(&total)
	if err != nil {
		return nil, 0, err
	}
	if !found {
		total = 0
	}
	// 链式 Add: 每段占位符从 #{1} 独立计数（分页段不再手工编号）
	var rows []Edge
	q := s.db.Add(`SELECT * FROM edges WHERE `+where, args...).
		Add(`ORDER BY sort, id LIMIT #{1} OFFSET #{2}`, size, (page-1)*size)
	if err := q.List(&rows); err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
