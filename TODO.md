# gcm — 实体-关系 CMS（从零构想落地）

> 设计文档: `design.md`（graph-layer.md 副本，无包袱构想版）
> 核心: 一张节点表（nodes）+ 一张引用表（edges）+ 一个类型系统。
> 三条界限: 不做图算法 / 不物化推理 / schema 不运行时自由。

## 里程碑与验收

### M0 项目脚手架
- [ ] go.mod（github.com/kran/gcm，replace 本地 dba/cho）
- [ ] design.md 已就位（✓）
- [ ] migrations/00001_init.sql: nodes / edges / accounts / settings 四表
- [ ] 依赖离线可构建（go build ./... 通过）
- 验收: `go build ./...` 零错误

### M1 类型系统（一切的地基）✅
- [x] types 包: TypeDef 配置解析（YAML）
- [x] 字段 kind: string / text / richtext / number / bool / image / file / ref / ref[]
- [x] 字段校验: to 类型存在、ref 目标类型匹配、required、未知字段拒绝
- [x] 代数声明: inverse / symmetric / transitive / equivalence（字段顶层）
- [x] 代数互斥校验 + inverse 双向校验（指向回当前类型）
- [x] URL 模式 / 模板级联名（node--{type}.html）/ FTS 参与 的类型级配置
- [x] 值校验: ref 收 id、ref[] 收数组、标量类型匹配
- 验收 ✅: 合法定义 + 15 个非法用例表驱动全绿; go vet/gofmt 干净

### M2 核心引擎（节点 CRUD）✅
- [x] core 包: Node 结构（id/type/slug/status/sort/fields, Fields Scan/Value）
- [x] Create（ref 字段自动落 edges, 目标存在+类型匹配校验）/ Update（全量替换边）/ Get / List
- [x] Delete: 显式清全部出/入引用 + 删节点（事务, fail-loud）
- [x] slug 唯一约束（部分唯一索引, 空 slug 共存）、status 发布过滤、分页
- [x] slug/status/sort 作为隐式列字段（类型定义保留名, 直接是 Node 列字段）
- [x] review 清理: Kind.Class() 三态（合并 Storage+Shape, 引擎零推断）、
      ToID 共享（types 导出, 删 core 重复）、Names 字典序、注释对齐
- 验收 ✅: 8 个单测全绿（CRUD/级联/slug 冲突/ref 校验/更新替换边/列表过滤）

### M3 引用（edges）✅
- [x] AddRef（公开写入口, 完整校验）/ RemoveRef（按 id, 缺失 fail）
- [x] OutRefs（出边, 按宿主类型查字段）/ InRefs（入边, 全局查字段 — inverse 原语）
- [x] symmetric 字段: 存一条, Out/In 双向展开
- [x] Merge(from, to): 出/入引用改指向 + 冲突去重（保留 to 的）+ 删源（事务）
- 验收 ✅: 6 个新单测（公开校验/出边入边/对称/删边/合并/合并错误）+ 既有 14 全绿

### M4 代数原语 ✅
- [x] symmetric: 双向展开（M3 完成, OutRefs/InRefs 存一条查两向）
- [x] transitive: Traverse（出边, 祖先链）+ Subtree（入边, 子树）— 递归 CTE + maxHops 防环
- [x] equivalence: EquivalenceClass（出+入双向递归, 类内全可达, 含起点）
- [x] inverse: InRefs 按 to_node 查（M3 完成）
- 验收 ✅: 6 个新单测（向上/向下/maxHops/等价类/环防/校验）+ 既有 21 全绿

### M5 查询语言（受限路径，成败点）
- [ ] lexer/parser: where 子句 / 路径段（field ->field->）/ 闭包（*）/ 基数（count）/ 穿透过滤
- [ ] 编译到有界 SQL（JOIN edges 链 + 递归 CTE，深度受限）
- [ ] 试金石查询集 #1-#7 全部可表达（design.md §6）
- 验收: 试金石集编译 + 执行正确；不可表达的查询 fail-loud

### M6 渲染层 ✅（引擎部分）
- [x] tpl 引擎移植（cmx: partial 组合/热加载/级联/partialOr/sprig 安全子集）
- [x] 查询函数注入: 查询定义在 Go 层（get/list/outRefs/inRefs/traverse/subtree/equivalence）,
      模板一行调用, 作者不写 SQL; 错误 panic → 渲染 fail-loud
- [x] 级联: node--{type}.html → node.html
- [ ] 渲染错误 HTML 注释不泄漏（待 Web 层 renderHTML 适配）
- 验收 ✅: 级联 + 注入函数 + fail-loud 3 测试

### M7 Web / admin（前台完成, admin 进行中）
- [x] 路由: /node/{id|slug}（数字按 id, 字母按 slug）+ 静态 /static/*
- [x] 404 统一出口（404.html 或纯文本）
- [x] 渲染失败 → HTML 注释（不泄漏细节, 日志记录）
- [x] admin 后端: 登录（accounts+bcrypt+session cookie）/ 登出 / me / types
- [x] 按 type 的实体管理 API: 列表/创建/详情/更新/删除（ref 字段经 fields 提交, 引擎落边）
- [x] 实体搜索 API（slug/fields LIKE, FTS 后续）
- [x] admin UI: no-build Vue 照搬（Element Plus + vue3-sfc-loader + Quill + less）
- [x] 类型管理变化: nodes.vue 重写为"左侧 type 列表 + 列表 + 编辑弹窗"
      （slug/status/sort + FieldRenderer 类型字段）
- [x] 引用编辑器: FieldRenderer 加 ref/ref[] 分支（实体搜索远程选择器, $api.search）
- [x] 树视图: TypeDef.View（tree/list + 自引用校验）+ admin tree API（全量 FullFields）
      + nodes.vue el-table 树形（默认展开, 行操作: 编辑/新建子/删除）
- [x] title 投影列（方案 C）: TypeDef.Title 声明字段 → nodes.title 列（列表/搜索/排序）,
      单事务双存同步（fields 保留完整）; search 改 title 列精确匹配
- [x] Expand/ExpandIn: 沿引用字段带信息递归展开（NodesOut/NodesIn 递归在 Node 上,
      环防护 + depth 有界; 出入分开）
- [x] 全文检索（FTS5 + bigram 预分词）: SearchIndex 接口（换引擎零改动）; 类型
      search:true + 已发布才进索引（业务规则在 Service, 引擎无脑执行）; bigram
      子串级精确（phrase 连续序列, 2字词天然支持）; bm25 排序; Rebuild; 模板
      search 函数 + example 搜索页; 踩坑: FTS5 无 UPSERT / JOIN 列歧义走子查询
- [x] expand 爆炸防护: 单字段 1000 上限 fail-loud（原来静默截断!）; 链深 ≤ 4;
      批量版 LIMIT max+1 探测; 语义澄清 — expand 沿出边字段走, 分类下的文章是
      入边不会误展开; 回归测试 2 个
- [x] 导航高亮范例（viicn 需求）: 引擎原语组装 — Go 层注册 activeChain
      （outRefs 找所属分类 + traverse 祖先链）, 模板只 has 判断; web.Site.Func 转发
- [x] 删除 NodesOut/NodesIn + Expand/ExpandIn（历史遗留: 生产零消费者, ExpandPath
      全能力覆盖 — 入边 <- / 多级 . / 批量 Many; 树形结构经递归路径表达式表达,
      防环由表达式长度有界保证; 净减 285 行含测试）
- [x] 核心抽象统一（哲学一致性盘点）: 统一路径语言 types.ParsePath（filter/expand/
      title 三处解析合一, "$." 前缀标记非分隔符的 bug 顺手揪出）; 列集合单份
      （types.IsNodeColumn）; 字段查找单份（types.FieldByName）; id 转换单份
      （core.ToID, render 本地副本删除）; expand/title 语义校验各层自持
- [x] title 声明支持穿透路径（employment: person.$.name — 引用目标字段投影,
      语法与 filter 一致; types 跨类型校验; 写时快照, 列表默认 expand 兜实时）
- [x] admin 列表默认展开全部出边 ref 字段（批量 ExpandPathMany; autoExpandExpr 复用）
- [x] 代码卫生（API 组织/健壮性盘点后修复）: FullFields 去 1000 截断（refTargetIDs
      一条 SQL 全量）; Update 内部拷贝不再改调用方入参; admin 错误出口统一
      internal/bad（slog 日志+透传）; DB()/Get 语义文档化; 回归测试 2 个
- [x] Filter 引擎（记录 API 查询参数）: 中缀表达式 + {:占位符} + 类型驱动编译 —
      列/JSON($.)/引用(~)/穿透(.$.)/出入边(<-)/逻辑; dba #{n} 占位符参数化防注入
- [x] ExpandPath: PocketBase 风格表达式（"authors, <-categories, employment.org"）—
      逗号并行 + 点号串行 + "<-" 前缀入边; 形态由类型定义驱动
      （ClassRef → 单值 *Node, ClassRefList → 数组）; Node.Expand map 容器
- [x] UI 静态服务（embed, no-cache）+ 上传 API（白名单）+ 改密
- 验收 ✅: 前台冒烟 + admin 8 测试（登录/CRUD/搜索/types/UI/上传）

### M8 示例站点 ✅
- [x] example/: 站点项目形态（main.go 组装: db+迁移+types+core+render+web+admin）
- [x] 类型定义: category(树)/article/person/org/employment(关系节点)
- [x] seed: 分类树 + 专家 + 文章（authors/categories 引用）+ 任职关系节点
- [x] 首页: 最新文章 + 作者（outRefs）+ 顶级分类导航
- [x] 分类页: /category/{slug} → SubtreeIDs 遍历 + inRefs 内容列表
- [x] 专家页: 任职经历（inRefs person + outRefs org）+ 发表文章（inRefs authors）
- 验收 ✅: 全站 7 路由冒烟 0 渲染错误; 途中修 get 兼容 float64 id、
      模板 ref 字段误用 .Fields（引用在 edges 不在 fields）
- [x] benchmark (core/bench_test.go): 10w nodes + 50w edges — 单点 44µs /
      出边 104µs / 反向 297µs / 子树 143µs / 组合 1.5ms; 列表 23ms(COUNT 主导)
- [x] 实战产出: ANALYZE 固化进 migrations.Up（统计缺失 = 计划灾难,
      组合查询 1.2s → 1.5ms, 820 倍）

### M9 文档收尾
- [ ] README: 概念（实体/引用两态/代数）、快速开始、三条界限
- [ ] 示例密码清理（上线前）
- [ ] 标记 v0.1.0（如需要）

## 原则（全程）

- 最笨但可迁移: 每步可独立交付、可测试，无过渡形态
- fail-loud: 配置/写入/查询错误响亮上抛，不静默
- 存的就是真的: 不物化推理，传递闭包只在查询时展开
- 引用是唯一原语: 不新增关系表；关系节点 = nodes 的 type
