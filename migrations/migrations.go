// Package migrations 内置 schema 演进（goose）。
//
//	迁移文件 embed 在库内, 引擎启动时对站点库自动执行（迁移失败 = 拒绝启动,
//	fail loud）。00001 是基线（裸 CREATE, 无 IF NOT EXISTS）: 旧的无版本库
//	直接冲突报错, 不存在"静默旧结构"。
package migrations

import (
	"context"
	"embed"
	"fmt"

	"github.com/kran/dba"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// Up 对 db 执行全部待应用迁移。失败响亮报错。
func Up(db *dba.SQL) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.Pool().DB, FS)
	if err != nil {
		return fmt.Errorf("migrations: provider: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("migrations: up: %w", err)
	}
	// ANALYZE: 统计缺失会让 SQLite 选错执行计划（实测: 组合查询
	// edges 全扫 1.2s vs 索引直查 1.5ms）。建库后跑一次, 规划器有据可依。
	if _, err := db.Add("ANALYZE").Exec(); err != nil {
		return fmt.Errorf("migrations: analyze: %w", err)
	}
	return nil
}
