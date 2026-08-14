# gcm — 实体-关系 CMS 引擎

Go 单二进制多站 CMS 库。核心模型:**nodes + edges + types**——节点（单表 + JSON fields）、引用（边）、类型系统（YAML 声明,kind 驱动）。

**定位**:开发者工具,不是给运营直接用的 CMS。模板作者不写查询（函数在 Go 层注入）,站点开发者负责全部组装。

## 核心模型

```
nodes   单表: id/type/title/slug/status/sort/fields(JSON)/created_at/updated_at
edges   引用表: from_node/field/to_node/sort — 出边(→)与入边(←)同一条边
types   YAML 声明: 类型/字段/kind/标题列/搜索
```

- 标量字段 → `fields` JSON 列;引用字段（`ref`/`ref[]`）→ 边
- 类型校验在**写路径**（CreateNode/AddEdge 归属校验）;查询宽松（拼错静默或 SQL 执行期报错）

## types.yaml

```yaml
types:
  category:
    title: name            # 标题列映射
    search: true           # 参与 FTS
    fields:
      - { name: name, kind: text }
      - { name: parent, kind: ref, to: category }
  article:
    title: title
    fields:
      - { name: title, kind: text, required: true }
      - { name: body, kind: richtext }
      - { name: featured, kind: bool }
      - { name: authors, kind: "ref[]", to: person }
      - { name: categories, kind: "ref[]", to: category }
  comment:
    fields:
      - { name: body, kind: text }
      - { name: article, kind: ref, to: article }   # 入边身份 = 来源类型.字段
```

内置 kind:`text` `textarea` `richtext` `number` `bool` `date` `upload-image` `upload-file` `ref` `ref[]` `array` `object`。自定义 kind 一个文件注册。

## Lisp filter（查询表达式）

```lisp
(= status 1)                      ; 列比较
(= $featured true)                ; JSON 字段（$name 严格, 无点）
(in status [1 2 3])               ; 标量 IN（数组字面量）
(edge ->parent {:id})             ; 出边折叠（目标身份）
(edge <-comment.article)          ; 入边存在性（类型.字段显式来源）
(edge ->categories (= $name "根"))  ; 出边目标谓词（开层）
(edge ->categories (edge ->parent (and (= status 1) (= $name "根"))))  ; 穿透链+中间条件
(in ->categories {:ids})          ; 出边集合（占位符数组）
(in ->categories (subtree "root")) ; 图原语集合
(and ...) (or ...) (not ...)      ; 逻辑
```

**前缀体系**:

```
status          列（nodes.status）
$name           JSON 字段（json_extract）
->categories    出边引用（宿主隐含）
<-comment.article  入边引用（来源类型显式 — 被指向方无需声明）
```

**规则**:
- 方向在前缀（`->` 出 / `<-` 入）,`edge` 是唯一引用操作符（一元=存在性,目标=值折叠/谓词开层）
- 开层 = 目标节点上下文（别名 t1/t2 逐层独立）;折叠 = 目标身份直接 `to_node = ?`（免 JOIN）
- 编译器宽松:不做字段校验（拼错延迟到 SQL 执行 fail-loud）;`$.`/裸 `<-field` 语法层拒绝
- 集合来源:数组字面量 `[..]` / 占位符 `{:name}` / 图原语 `(subtree "slug")` / 自定义函数

**注册驱动**:`RegisterLispFuncC(name, fn)` — 站点自定义函数进表达式（与内置同注册表）。

## 图原语（Go 层）

```go
svc.Traverse("category", id, "parent", 20)   // 祖先链 id（id 序）
svc.Ancestors("category", id, "parent", 20)  // 祖先链 []*Node（深度序 根→叶）
svc.Subtree("category", id, "parent", 20)    // 子树 id
svc.OutEdges(type, id, field, page, size)    // 出边（symmetric 双向）
svc.InEdges(to, field, page, size)           // 入边（宽松, 不校验）
svc.ExpandPath(id, "authors, categories")    // 路径展开（批量: ExpandPathMany）
```

图原语**不跨类型**——typeName 显式传（字段归属校验,纯内存）;入边跨类型是语义。

## 结构化查询

```go
list, total, err := svc.ListQ(core.ListQuery{
    Filter: `(and (= type "article") (edge ->categories (= $name "根")))`,
    Sort:   "created_at DESC",
    Expand: "authors, categories",
    Page:   1, Size: 20,
}, params)  // params 绑定 {:name} 占位符（可选）
```

- 类型过滤由使用方构建（`(= type "x")`）——ListQuery 不预设
- 分页走 dba.Page 协议（`${F:*}`/`${order:}` 槽,count/data 同 base 分叉,filter 编译一次）

## 记录 API

```
GET /api/nodes/{type}?filter=<lisp>&sort=&page=&size=&expand=
→ {"items": [Node...], "total": N}
```

sort 列白名单（外部边界防 ORDER BY 注入）;filter 编译错误 → 400 fail-loud。

## 渲染与站点

- 模板级联 `node--{type}.html` → `node.html` → `404.html`（无主题,自建）
- 页面上下文:`SiteSpec.PageDataMaker` 泛型（每站自己的 PageData 类型,gcm 不预设字段）
- Hook 总线:`HookRender`（页面级）/ `HookNodeRender`（节点页）/ `HookNodeEnrich`（节点数据,默认注入 `Extra["url"]` = `/node/{slug|id}`）
- 站点模板函数:`site.Func(name, fn)` — 模板一行调用,查询在 Go 层
- 单站 `gcm.NewApp[T]` + 多站 `web.HostMux`

## 启动

```go
app, err := gcm.NewApp(gcm.Options{AdminPass: "..."}, gcm.SiteSpec[*PageData]{
    Hosts:     []string{"example.com"},
    DBPath:    "site.db",
    Types:     yamlBytes,
    Templates: "templates",
    Static:    "static",
    Uploads:   "uploads",
    PageDataMaker: func(svc *core.Service, ctx *web.CmsCtx, node *core.Node) *PageData { ... },
    Setup:     setup,  // 站点模板函数 + 自定义路由
})
app.Listen(":8080")
```

管理后台 `/admin`（登录/节点 CRUD/类型树/设置;字段渲染按 kind 驱动,未知 kind 动态加载 `ui-extras/{kind}.vue`）。

## 哲学

- **最笨但可迁移**:无 IR、无查询计划、无防御——表达式树 = SQL 树 1:1
- **fail-loud**:语法错误响亮;查询错误宽松（用者负责制）
- **注册驱动**:kind / Lisp 函数 / Hook / 站点函数——每层一个扩展口
- **类型校验只在写路径**;查询层零校验
