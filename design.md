# 实体-关系 CMS 设计（从零构想，无历史包袱）

> 状态: **构想**（未实施）
> 定位: 探索"强关系 CMS 该长什么样"的设计文档——从零开始，
> 不兼容任何既有系统。核心概念只有两个：**实体（nodes）** 与
> **引用（edges）**，一切差异都在类型定义里。
> 思想内核: **关系即节点**——有身份的引用是节点，无身份的引用是边。

## 0. 探索判据

1. **边界经不经得起推敲**——实体/引用/凭据/动作的每一刀都有物理依据
2. **张力是不是真的**——引用两态、代数原语、查询表达力上界，是没有教科书答案的地方
3. **可证伪**——试金石查询集：任何查询在路径语言下长得像 Cypher 或退化为手写 SQL，方向即被裁决

**想清楚可以到极限，建造必须由需求牵引。**

## 1. 概念模型：世界只有两种东西

```
实体（nodes）—— 一切有身份的个体：文章、分类、专家、任职、活动……
               type 区分；fields 由类型定义驱动
引用（edges）—— 实体之间的关系：无身份的引用（authors/related/parent）
凭据（外置）—— accounts（后台）/ site_users（前台）：认证语义，不进实体
动作（站点业务表）—— messages / favorites：交互痕迹，量大、无 URL
```

### 引用两态（核心判据：身份）

| | 无身份引用 | 有身份引用 |
|---|---|---|
| 形态 | `article.authors: ref[]` 字段 | `employment` 关系节点类型 |
| 存储 | edges 行 | nodes 行（type='employment'） |
| 可寻址/有 URL | ✗ | ✓ |
| 自己的字段/生命周期 | ✗ | ✓ |
| 何时用 | 纯关系，无属性/生命周期需求 | 关系本身是内容（任职/合作/合同） |

**判断前移到类型定义时**：schema 设计者决定 `ref` 字段还是关系节点类型——
低频、可版本化、可测试。运行期零判断。

### 这一刀治好的历史病

| 病 | 解药 |
|---|---|
| "边何时升级实体"人肉判断 | 判断前移到类型定义 |
| props EAV 后门 | 关系节点属性 = nodes.fields，同一套类型校验 |
| kind 自由字符串膨胀 | kind 收编为字段名（有 to/代数/校验背书） |
| 多对多关联表爆炸 | edges 一张表承载一切无身份引用 |

## 2. 数据模型：4 张核心表

```sql
-- ① 节点表: 一切实体（内容 / term / 关系节点）
CREATE TABLE nodes (
    id         INTEGER PRIMARY KEY,
    type       TEXT    NOT NULL,           -- article / category / person / employment ...
    slug       TEXT,                       -- URL 段（类型可配置）
    status     INTEGER NOT NULL DEFAULT 0, -- 发布状态（通用：term 也有隐藏状态）
    sort       INTEGER NOT NULL DEFAULT 0,
    fields     TEXT    NOT NULL DEFAULT '{}',  -- 类型特有字段（类型系统校验）
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_nodes_type ON nodes(type, status, sort);

-- ② 引用表: 无身份引用（引擎内部实现，类型系统只见"引用字段"）
CREATE TABLE edges (
    id         INTEGER PRIMARY KEY,
    from_node  INTEGER NOT NULL REFERENCES nodes(id),
    field      TEXT    NOT NULL,           -- 类型定义里的引用字段名（非自由字符串）
    to_node    INTEGER NOT NULL REFERENCES nodes(id),
    sort       INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    UNIQUE (from_node, field, to_node)
);
CREATE INDEX idx_edges_from ON edges(from_node, field);  -- 正向
CREATE INDEX idx_edges_to   ON edges(to_node, field);    -- 反向（inverse）

-- ③ 后台凭据（外置）
-- accounts: id | username | password_hash | status ...
-- ④ 前台凭据（未来，外置）
-- site_users: id | username | password_hash | node_id(对应 person 节点) ...
```

**引擎设施（不算概念）**：迁移版本表、FTS 虚拟表（按类型选择性建）、
settings 键值（站点级配置，引擎内部）。

**站点业务表 N 张**（业务层拥有）：messages、favorites——引用实体时可存 nodes.id，
但表本身不属于核心。

## 3. 类型系统：唯一 schema

```yaml
types:
  article:
    fields:
      - { name: body,  kind: richtext, required: true }
      - { name: cover, kind: image }
      - { name: authors,    kind: ref[], to: person,   inverse: articles }  # 反向自动可查
      - { name: related,    kind: ref[], to: article,  symmetric: true }    # 存一次查双向
      - { name: categories, kind: ref[], to: category, transitive: true }   # 归属即引用
  category:
    fields:
      - { name: name,   kind: string, required: true }
      - { name: parent, kind: ref,    to: category, inverse: children }     # 树 = 引用
      - { name: banner, kind: image }
  person:
    fields:
      - { name: name, kind: string }
      - { name: employment, kind: ref[], to: employment }
  employment:                                    # 关系节点 —— 与 article 同等待遇
    fields:
      - { name: person, kind: ref, to: person, required: true }
      - { name: org,    kind: ref, to: org, required: true }
      - { name: role,   kind: string }
      - { name: tenure, kind: string }           # 任期是字段，不是 props
```

- 类型定义驱动一切：字段校验、URL 模式（/article/{slug}、/category/{slug}）、
  模板级联（node--{type}.html）、FTS 参与、admin 分组
- **树 = 引用的特例**：`category.parent: ref`，子树查询 = transitive 遍历；
  导航菜单 = 引擎原语，不再是特殊结构

### 代数 → 引擎原语（存储只存一条，查询时展开）

| 代数 | 查询行为 | 原语 |
|---|---|---|
| inverse | 反向查询（"此人写了哪些文章"） | 按 to_node 查 |
| symmetric | 双向展开，不双存 | from=id OR to=id |
| transitive | 可达性（任意深度） | 递归 CTE 遍历 |
| equivalence | 等价类展开（并查集语义） | 非边遍历 |
| 无代数 | 普通出/入引用 | 直查 |

**代数也是"该不该是边"的探测器**：纯代数、无属性/生命周期的引用
（synonym）→ 退回主节点属性（fields.synonyms，搜索时展开）。

## 4. 查询表达力：受限路径语言

不是固定函数，不是 Cypher——**沿引用字段的路径 + 过滤**，声明式：

```
article
  where category ->broader* contains "制造业"     // 传递闭包
  where count(authors) >= 3                        // 引用基数过滤
  where authors -> where(fields.level = "senior")  // 沿引用穿透过滤
```

**表达力上界：能编译成有界 SQL（JOIN edges 链 + 深度受限的递归 CTE）。**
过了这条线 → 你要的不是 CMS 是图数据库，诚实的答案是 Neo4j。

编译目标 = 查询管道（模板一行）：`{{ nodes | query "article where category ->broader* contains '制造业'" | fetchList }}`

## 5. 三条界限（原则性停止线）

**界限一：不做图算法。** 最短路径/社群发现/中心度——定义上不做。
每个查询必须编译成一条有界 SQL、在请求周期内返回。需要全图算法的需求 =
导出到图数据库的信号。

**界限二：不物化推理。** 不做关系触发器、不物化推理结果。
**存的就是真的，没有隐藏的派生真相**——传递闭包只在查询时展开
（`->broader*`），绝不在存储时物化。推理是本体系统（OWL）的领域。

**界限三：schema 不运行时自由。** 类型定义（含代数）在配置里、走迁移。
代数被运行时改动 → 存量数据语义漂移。schema 是开发者的、低频的、版本化的。

## 6. 试金石查询集（可证伪点）

| # | 查询 | 路径语言表达 |
|---|---|---|
| 1 | 被 3 位以上专家关联的案例 | `where count(authors) >= 3` |
| 2 | 属于制造业或其同义词的内容 | `where category ->synonym* contains "制造业"` |
| 3 | 某专家的高级别文章 | `where authors -> where(fields.level = "senior")` |
| 4 | 专家 A 的文章引用了哪些分类 | `authors -> article -> category`（两跳） |
| 5 | 相关案例的案例 | `related -> related`（symmetric） |
| 6 | 沿 broader 两跳内的所有分类 | `->broader*`（transitive，深度受限） |
| 7 | 2024 后仍有效的任职 | 关系节点字段过滤（employment.tenure） |
| 8 | 合并两个专家节点 | `MergeEntity` 事务原语 |
| 9 | 展示"任职"为独立页面 | 关系节点类型——无需升级，一开始就是实体 |

**裁决**：#1-#7 若都能编译成有界 SQL → 方向成立；超过半打专用原语仍不够 → 证伪。

## 7. 核心引擎接口

```go
// 一个统一服务：节点与引用不可分（field 属于类型定义）
type Service struct {
    types TypeSystem // 类型定义（配置，版本化）
    db    *dba.SQL
}

// ── 节点 ─────────────────────────────
func (s *Service) Create(type_ string, fields map[string]any) (int64, error) // 含 ref 字段落 edges
func (s *Service) Update(id int64, fields map[string]any) error
func (s *Service) Get(id int64) (*Node, error)

// ── 引用 ─────────────────────────────
func (s *Service) AddRef(from, to int64, field string, sort int) (int64, error)
func (s *Service) RemoveRef(id int64) error
func (s *Service) OutRefs(id int64, field string, page, size int) ([]Edge, int64, error)
func (s *Service) InRefs(id int64, field string, page, size int) ([]Edge, int64, error)  // inverse
func (s *Service) Traverse(start int64, field string, maxHops int) ([]int64, error)      // transitive
func (s *Service) EquivalenceClass(id int64, field string) ([]int64, error)              // equivalence

// ── 一致性 ───────────────────────────
func (s *Service) Delete(id int64) error         // 删节点 + 清全部出/入引用（级联）
func (s *Service) Merge(from, to int64) error    // 合并: 引用改指向 + 删源（事务）

// ── 查询 ─────────────────────────────
func (s *Service) Query(path string, args ...any) (*Query, error) // 路径语言 → 有界 SQL
```

**校验（fail-loud）**：类型存在、字段合法（to 类型匹配）、ref 目标存在、
代数一致性（symmetric 不双存、inverse 不手写反向）、UNIQUE 兜底。

## 8. 能力对比：比"树 + 标签"强多少

| 需求 | 树+标签 | 本设计 | 差距 |
|---|---|---|---|
| 导航/归档/单分类 | 100 | 100（树 = 引用特例） | 0 |
| 多标签归属 | 90 | 95（ref[] 字段） | 5% |
| 内容引用（作者/相关） | 85 | 95（ref + inverse） | 10% |
| **关系作为内容**（任职/合作/合同） | **30** | **95**（关系节点） | **65 分——差距所在** |
| 组合查询 | 60 | 90（路径语言） | 30% |
| 图算法/推理 | ✗ | ✗（界限内都不做） | 0 |

**强多少 = f(内容形态)**：纯内容站 ≈ 5-10%；内容类型丰富且互引 ≈ 30-50%；
关系本身就是内容（知识库/档案/图谱）≈ 3-5 倍。

## 9. 分层

```
┌─────────────────────────────────────────────────┐
│ 业务层（站点项目）：handler 组合引擎 + 站点业务表 │
├─────────────────────────────────────────────────┤
│ Web 层：路由 + admin（按 type 分组的实体管理）    │
├─────────────────────────────────────────────────┤
│ 渲染层（core/render）：路径语言编译 + 模板        │
├─────────────────────────────────────────────────┤
│ 核心引擎（core）：类型系统 + 节点 + 引用 + 原语   │
├─────────────────────────────────────────────────┤
│ 底座：dba / hook / migrations                    │
└─────────────────────────────────────────────────┘
```

无包袱设计下"图层/CMS 层"合一：引用字段属于类型定义，节点与引用是
**一个引擎的两面**，不存在互相注入。核心不认识业务，只认识类型与引用。

## 10. 非目标与演进

- **图算法** → 导出到图数据库的信号（nodes/edges 直接映射 vertex/edge）
- **推理物化 / 关系触发器** → 存的就是真的
- **schema 运行时自由** → 配置 + 迁移，版本化
- **超图/任意元数** → 关系节点表达（它是节点不是超边）
- **查询语言发明（Cypher 级）** → 表达力上界 = 有界 SQL
- **前台用户** → site_users 凭据外置；用户动作（投稿/收藏）通过 person 节点引用进图
- **站点业务表** → 业务层自建（messages/favorites），可引用 nodes.id，不进核心

## 11. 实施路径

从零设计无两阶段包袱：**核心 = nodes + edges + 类型系统 + ref/inverse** 本身就是最小完整形态。
顺序：

1. **类型系统**（配置解析 + 字段校验 + ref 定义）——一切的地基
2. **核心引擎**（节点 CRUD + 引用 CRUD + Delete/Merge 一致性）
3. **代数原语**（symmetric/inverse/transitive/equivalence 查询展开）
4. **路径语言编译**（→ 有界 SQL）
5. **渲染层 + Web/admin**（按 type 的模板级联 + 实体管理）
6. **业务用例**（zhiqi 案例↔企业、viicn 专家↔文章——第一刀见血）

> 每步可独立交付、可测试；真实需求牵引步进，但每一步都是终局的一部分，
> 没有"过渡形态"——这是放弃包袱后的最大简化。
