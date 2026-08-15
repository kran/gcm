<template>
  <div class="nodes-page">
    <!-- 左侧 type 列表（类型定义驱动, 动态） -->
    <div class="nodes-tree">
      <div class="nodes-tree-header">
        <span style="font-weight:600;font-size:14px;">类型</span>
        <el-button link type="primary" size="small" @click="loadTypes">刷新</el-button>
      </div>
      <div class="type-list">
        <div v-for="t in typeNames" :key="t"
             class="type-item" :class="{ active: query.type === t }"
             @click="selectType(t)">
          <el-icon :size="15"><component :is="typeIcon(t)" /></el-icon>
          <span>{{ t }}</span>
        </div>
      </div>
    </div>

    <!-- 树过滤栏（当前类型有指向树结构的 ref 字段时显示） -->
    <div v-if="filterTree.def" class="nodes-tree" style="width:180px;">
      <div class="nodes-tree-header">
        <span style="font-weight:600;font-size:14px;">按{{ filterTree.label }}过滤</span>
        <el-button v-if="filterTree.active" link type="primary" size="small" @click="clearTreeFilter">清除</el-button>
      </div>
      <div class="type-list" style="padding:0;">
        <div class="type-item" :class="{ active: filterTree.active === 0 }" @click="clearTreeFilter">
          <span>全部</span>
        </div>
        <el-tree :data="filterTree.nodes" node-key="id" default-expand-all
                 :expand-on-click-node="false" highlight-current
                 :current-node-key="filterTree.active" @node-click="onTreeClick">
          <template #default="{ data }">
            <span class="tree-node-label" :style="{ fontWeight: filterTree.active === data.id ? 700 : 400 }">
              {{ titleOf(data) }}
            </span>
          </template>
        </el-tree>
      </div>
    </div>

    <!-- 右侧列表 -->
    <div class="nodes-list">
      <div style="display:flex;gap:8px;align-items:center;margin-bottom:14px;flex-wrap:wrap;">
        <span style="font-size:16px;font-weight:600;">{{ query.type || '未选择类型' }}</span>
        <el-select v-model="query.status" placeholder="状态" size="small" style="width:110px"
                   clearable @change="refresh">
          <el-option label="已发布" :value="1" />
          <el-option label="草稿" :value="0" />
        </el-select>
        <el-input v-model="query.q" placeholder="搜索标题/别名" size="small" style="width:180px"
                  clearable @change="refresh" />
        <el-button size="small" @click="refresh">刷新</el-button>
        <el-button type="primary" size="small" :disabled="!query.type" @click="createVisible = true">
          新建 {{ query.type || '' }}
        </el-button>
      </div>

      <!-- 树视图（view: tree 类型, 全量不分页; el-table 树形模式, 行操作: 编辑/新建子/删除） -->
      <el-table v-if="treeMode" :data="treeNodes" v-loading="loading" row-key="id"
                :tree-props="{ children: 'children' }" default-expand-all border stripe size="small">
        <el-table-column label="标题" min-width="220">
          <template #default="{ row: r }"><span style="font-weight:600;">{{ titleOf(r) }}</span></template>
        </el-table-column>
        <el-table-column label="slug" min-width="140">
          <template #default="{ row: r }"><code>{{ r.slug || '#' + r.id }}</code></template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row: r }">
            <el-tag :type="r.status === 1 ? 'success' : 'info'" size="small">
              {{ r.status === 1 ? '已发布' : '草稿' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row: r }">
            <node-ops :node="r" :defs="typeDefs" :type-name="query.type" show-create
                      :parent-id="r.id" @changed="refresh" />
          </template>
        </el-table-column>
      </el-table>

      <el-table v-else :data="rows" v-loading="loading" border stripe size="small">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="标题" min-width="200">
          <template #default="{ row: r }"><span style="font-weight:600;">{{ titleOf(r) }}</span></template>
        </el-table-column>
        <el-table-column label="slug" min-width="140">
          <template #default="{ row: r }"><code>{{ r.slug || '#' + r.id }}</code></template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row: r }">
            <el-tag :type="r.status === 1 ? 'success' : 'info'" size="small">
              {{ r.status === 1 ? '已发布' : '草稿' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="sort" label="排序" width="70" />
        <el-table-column label="更新时间" width="165">
          <template #default="{ row: r }">{{ fmt(r.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="170" fixed="right">
          <template #default="{ row: r }">
            <node-ops :node="r" :defs="typeDefs" :type-name="query.type" @changed="refresh" />
          </template>
        </el-table-column>
      </el-table>

      <!-- 页面级新建（行编辑走 NodeOps） -->
      <node-edit-dialog :visible="createVisible" :type-name="query.type" :defs="typeDefs"
                        @close="createVisible = false" @changed="refresh" />

      <div v-if="!treeMode" style="display:flex;justify-content:flex-end;margin-top:12px;">
        <el-pagination background layout="total, prev, pager, next" :total="total"
                       :page-size="query.size" :current-page="query.page"
                       @current-change="onPageChange" />
      </div>
    </div>

<script>
export default {
    name: 'NodesPage',
    errorCaptured(err, info) {
        console.error('[nodes] errorCaptured:', err, info)
        return false
    },
    components: {
        FieldRenderer: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/FieldRenderer.vue')),
        NodeOps: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeOps.vue')),
        NodeEditDialog: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/NodeEditDialog.vue')),
    },
    data() {
        return {
            typeNames: [],
            typeDefs: {},
            rows: [],
            total: 0,
            loading: false,
            treeMode: false,
            treeNodes: [],
            parentField: 'parent',
            filterTree: { def: null, field: '', label: '', nodes: [], active: 0 },
            query: { type: '', status: null, q: '', page: 1, size: 20 },
            createVisible: false,
        }
    },
    async mounted() {
        console.log('[nodes] mounted')
        try {
            await this.loadTypes()
            console.log('[nodes] types loaded:', this.typeNames.length)
        } catch (e) { console.error('[nodes] loadTypes failed:', e) }
    },
    methods: {
        // 图标来自类型配置（icon 字段）; 空 = 默认
        typeIcon(t) {
            const def = this.typeDefs[t] || {}
            return def.icon || 'Files'
        },
        async loadTypes() {
            const res = await window.$api.types()
            this.typeDefs = res.types || {}
            this.typeNames = Object.keys(this.typeDefs).sort()
            // 默认选第一个
            if (!this.query.type && this.typeNames.length) this.selectType(this.typeNames[0])
        },
        selectType(t) {
            this.query.type = t
            this.query.page = 1
            this.query.filter = '' // 类型切换清残留（旧类型的字段对不上新类型, fail-loud 报错）
            const def = this.typeDefs[t] || {}
            this.treeMode = def.view === 'tree'
            this.setupFilterTree(def)
            if (this.treeMode) this.loadTree()
            else this.refresh()
        },
        // 自引用 ref 字段名（树组装用）: to == 自身类型的第一个 ref
        selfRefField(def) {
            const name = def && def.name
            const f = (def.fields || []).find(x => x.to === name)
            return f ? f.name : ''
        },
        async loadTree() {
            if (!this.query.type) return
            this.loading = true
            try {
                const res = await window.$api.get('/admin/tree', { type: this.query.type })
                this.parentField = this.selfRefField(this.typeDefs[this.query.type]) || 'parent'
                this.treeNodes = this.buildTree(res.items || [], this.parentField)
            } finally { this.loading = false }
        },
        // 树过滤: 当前类型的 ref 字段指向"有树结构"类型时, 提供按树过滤。
        // 点击树节点 → DFS 子树 id 集合 → filter "{field} ~ [ids]" → 刷新列表。
        async setupFilterTree(def) {
            this.filterTree = { def: null, field: '', label: '', nodes: [], active: 0 }
            if (!def) return
            const name = def.name
            for (const f of def.fields || []) {
                if (!(f.kind === 'ref' || f.kind === 'ref[]')) continue
                if (f.to === name) continue // 自身自引用: 列表即树（treeMode）, 过滤栏多余
                const tdef = this.typeDefs[f.to]
                if (tdef && this.selfRefField(tdef)) {
                    this.filterTree = { def: tdef, field: f.name, label: f.label || f.name, nodes: [], active: 0 }
                    this.loadFilterTree(f.to)
                    return // 第一个树引用字段
                }
            }
        },
        async loadFilterTree(typeName) {
            try {
                const res = await window.$api.get('/admin/tree', { type: typeName })
                const pf = this.selfRefField(this.filterTree.def) || 'parent'
                this.filterTree.nodes = this.buildTree(res.items || [], pf)
            } catch (_) {}
        },
        // 点击树节点: 子树集合 → filter 刷新列表（Lisp: 引用集合 in + 数组字面量）
        onTreeClick(n) {
            if (this.filterTree.active === n.id) return
            this.filterTree.active = n.id
            const ids = this.collectSubtree(n)
            this.query.filter = '(in ->' + this.filterTree.field + ' [' + ids.join(' ') + '])'
            this.query.page = 1
            this.refresh()
        },
        clearTreeFilter() {
            if (this.filterTree.active === 0 && !this.query.filter) return
            this.filterTree.active = 0
            this.query.filter = ''
            this.query.page = 1
            this.refresh()
        },
        collectSubtree(n) {
            const ids = [n.id]
            const walk = (node) => {
                ;(node.children || []).forEach(c => { ids.push(c.id); walk(c) })
            }
            walk(n)
            return ids
        },
        // 平铺节点列表 → 树（无 parent / parent 缺失 = 根）
        buildTree(items, parentField) {
            const map = {}
            items.forEach(n => { map[n.id] = { ...n, children: [] } })
            const roots = []
            items.forEach(n => {
                const node = map[n.id]
                const pid = n.fields && n.fields[parentField]
                const parent = pid && map[pid]
                if (parent) parent.children.push(node)
                else roots.push(node)
            })
            return roots
        },
        async refresh() {
            if (!this.query.type) return
            this.loading = true
            try {
                const params = { type: this.query.type, page: this.query.page, size: this.query.size }
                if (this.query.status !== null && this.query.status !== '') params.status = this.query.status
                if (this.query.q) params.q = this.query.q
                if (this.query.filter) params.filter = this.query.filter
                const res = await window.$api.nodes(params)
                this.rows = res.items || []
                this.total = res.total || 0
            } finally { this.loading = false }
        },
        onPageChange(p) { this.query.page = p; this.refresh() },
        fmt(s) { return s ? s.replace('T', ' ').slice(0, 16) : '' },
        // 列表标题: 统一走 $api.refLabel（title 列 → slug → 类型字段序兜底 → expand 合成）
        titleOf(r) {
            return window.$api.refLabel(r, this.typeDefs[r.type] || null)
        },
    },
}</script>

<style scoped>
.nodes-page { display: flex; gap: 16px; flex: 1; min-height: 0; }
.nodes-tree {
    width: 230px; flex-shrink: 0;
    border-right: 1px solid #eee; padding-right: 12px;
    overflow: auto;
}
.nodes-tree-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
.nodes-list { flex: 1; min-width: 0; }
.type-list { display: flex; flex-direction: column; gap: 2px; }
.type-item {
    display: flex; align-items: center; gap: 8px;
    padding: 6px 10px; border-radius: 6px; cursor: pointer;
    font-size: 13px; color: #555;
}
.type-item:hover { background: #f5f7fa; }
.type-item.active { background: #ecf5ff; color: #409eff; font-weight: 600; }
</style>
