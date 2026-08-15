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
	"io/fs"
	"os"

	"github.com/kran/dba"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var FS embed.FS

// Up 对 db 执行全部待应用迁移（gcm 内置表: nodes/edges/settings/admin）。失败响亮报错。
// gcm 内置迁移版本表名（与站点业务表迁移错开）。
const gcmVersionTable = "migr_gcm"

func Up(db *dba.SQL) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, db.Pool().DB, FS,
		goose.WithTableName(gcmVersionTable))
	if err != nil {
		return fmt.Errorf("migrations: provider: %w", err)
	}
	results, err := provider.Up(context.Background())
	if err != nil {
		return fmt.Errorf("migrations: up: %w", err)
	}
	// ANALYZE 只在新迁移应用后跑: 统计缺失会让 SQLite 选错执行计划
	// （实测: 组合查询 edges 全扫 1.2s vs 索引直查 1.5ms）。每次启动
	// 都 ANALYZE 对大库是秒级开销 — 无新迁移时 schema 未变, 无需重跑。
	if len(results) > 0 {
		if _, err := db.Add("ANALYZE").Exec(); err != nil {
			return fmt.Errorf("migrations: analyze: %w", err)
		}
	}
	return nil
}

// Runner 站点业务表迁移执行器（gcm 只管自己的表; 站点项目自己的表
// 用独立 goose provider + 独立版本表名 — 两套迁移互不干扰）。
//
// Setup 里跑:
//
//	runner := migrations.NewRunner(site.DB())
//	runner.Up(myFS, "my_goose_db_version")
type Runner struct {
	db *dba.SQL
}

// NewRunner 建迁移执行器（db 来自装配, 调用方不传）。
func NewRunner(db *dba.SQL) *Runner {
	return &Runner{db: db}
}

// Up 执行 fsys 里的全部待应用迁移。tableName 是版本表名（站点/插件迁移
// 用独立表名与 gcm 内嵌错开 — 建议 "site_goose_db_version"）。
func (r *Runner) Up(fsys fs.FS, tableName string) error {
	opts := []goose.ProviderOption{goose.WithTableName(tableName)}
	provider, err := goose.NewProvider(goose.DialectSQLite3, r.db.Pool().DB, fsys, opts...)
	if err != nil {
		return fmt.Errorf("migrations: provider: %w", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		return fmt.Errorf("migrations: up: %w", err)
	}
	return nil
}

// UpDir 从本地目录执行迁移（开发期改迁移文件即生效, 不用重新编译 embed）。
func (r *Runner) UpDir(dir string, tableName string) error {
	return r.Up(os.DirFS(dir), tableName)
}
