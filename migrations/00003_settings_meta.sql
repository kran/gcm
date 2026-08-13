-- +goose Up
-- settings 运营元数据: 分组（运营分类/未来权限挂载点）+ 数据类型（kind 名,
-- 编辑形态与校验复用类型系统）+ 描述（key 语义说明 — 时间长了不认识的解药）。
-- value 保持 JSON 列（各 kind 值形态不同, kind 决定编辑与校验而非存储格式）。
ALTER TABLE settings ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN kind TEXT NOT NULL DEFAULT 'string';
ALTER TABLE settings ADD COLUMN note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE settings DROP COLUMN group_name;
ALTER TABLE settings DROP COLUMN kind;
ALTER TABLE settings DROP COLUMN note;
