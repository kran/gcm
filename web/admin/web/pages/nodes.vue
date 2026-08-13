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
        <el-button type="primary" size="small" :disabled="!query.type" @click="openCreate">
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
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row: r }">
            <el-button link type="primary" size="small" @click="openEdit(r)">编辑</el-button>
            <el-button link size="small" @click="openCreateChild(r)">新建子</el-button>
            <el-button link size="small" @click="openExpand(r)">引用</el-button>
            <el-button link type="danger" size="small" @click="doDelete(r)">删除</el-button>
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
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row: r }">
            <el-button link type="primary" size="small" @click="openEdit(r)">编辑</el-button>
            <el-button link size="small" @click="openExpand(r)">引用</el-button>
            <el-button link type="danger" size="small" @click="doDelete(r)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="!treeMode" style="display:flex;justify-content:flex-end;margin-top:12px;">
        <el-pagination background layout="total, prev, pager, next" :total="total"
                       :page-size="query.size" :current-page="query.page"
                       @current-change="onPageChange" />
      </div>
    </div>

    <!-- 新建/编辑 -->
    <el-dialog v-model="dialog.visible" :title="dialog.isEdit ? '编辑 #' + dialog.id : '新建 ' + query.type"
               width="80vw">
      <el-form label-width="90px">
        <el-form-item label="slug">
          <el-input v-model="dialog.form.slug" placeholder="URL 段（留空 = /node/{id}）" />
        </el-form-item>
        <el-form-item label="状态">
          <el-radio-group v-model="dialog.form.status">
            <el-radio :value="1">已发布</el-radio>
            <el-radio :value="0">草稿</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="排序">
          <el-input-number v-model="dialog.form.sort" :min="0" />
        </el-form-item>
        <el-divider style="margin:8px 0 16px;" />
        <field-renderer v-if="dialog.def" :fields="dialog.def.fields" v-model="dialog.form.fields"
                        :ref-preset="dialog.form.refPreset || {}" :defs="typeDefs" />
      </el-form>
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <!-- 引用展开预览 -->
    <el-dialog v-model="expandDialog.visible" title="引用展开" width="60vw">
      <div v-loading="expandDialog.loading" style="min-height:120px;">
        <template v-if="expandDialog.node">
          <div v-if="!expandDialog.fields.length" style="color:#9ca3af;padding:20px;text-align:center;">
            该类型没有引用字段
          </div>
          <div v-for="field in expandDialog.fields" :key="field" style="margin-bottom:16px;">
            <div style="font-weight:600;font-size:14px;color:#409eff;margin-bottom:6px;">{{ field }}</div>
            <div v-for="n in expandItems(expandDialog.node.expand[field])" :key="n.id"
                 style="padding:8px 12px;background:#f9fafb;border-radius:6px;margin-bottom:4px;display:flex;align-items:center;gap:8px;">
              <el-tag size="small">{{ n.type }}</el-tag>
              <span style="font-weight:600;">{{ titleOf(n) }}</span>
              <code style="color:#9ca3af;font-size:12px;">{{ n.slug || '#' + n.id }}</code>
            </div>
          </div>
        </template>
        <div v-else-if="!expandDialog.loading" style="color:#9ca3af;padding:20px;text-align:center;">
          展开失败（字段可能不在该类型上）
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script>
import FieldRenderer from './FieldRenderer.vue'
import CatTree from './CatTree.vue'
export default {
    name: 'NodesPage',
    components: { FieldRenderer, CatTree },
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
            dialog: { visible: false, isEdit: false, id: 0, def: null, saving: false, form: emptyForm() },
            expandDialog: { visible: false, loading: false, node: null, fields: [] },
        }
    },
    async mounted() { await this.loadTypes() },
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
        // 点击树节点: 子树集合 → filter 刷新列表
        onTreeClick(n) {
            if (this.filterTree.active === n.id) return
            this.filterTree.active = n.id
            const ids = this.collectSubtree(n)
            this.query.filter = this.filterTree.field + ' ~ [' + ids.join(',') + ']'
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
            const label = (n) => ({ ...n, label: this.titleOf(n) })
            const walk = (list) => list.map(n => ({ ...label(n), children: walk(n.children || []) }))
            return walk(roots)
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
        openCreate(parentId) {
            this.dialog.isEdit = false
            this.dialog.id = 0
            this.dialog.def = this.typeDefs[this.query.type] || null
            this.dialog.form = emptyForm()
            if (parentId) this.dialog.form.fields.parent = parentId
            this.dialog.visible = true
        },
        openCreateChild(row) {
            this.openCreate(row.id)
        },
        // 引用展开预览: ExpandPath 全 ref 字段一层全景
        async openExpand(r) {
            this.expandDialog.visible = true
            this.expandDialog.loading = true
            this.expandDialog.node = null
            try {
                const res = await window.$api.get('/admin/expand', { node: r.id })
                this.expandDialog.node = res.node || null
                this.expandDialog.fields = this.expandDialog.node ? Object.keys(this.expandDialog.node.expand || {}) : []
            } catch (_) { this.expandDialog.node = null }
            this.expandDialog.loading = false
        },
        // 展开值形态: 单值（*Node）或数组（[]*Node）— 类型定义驱动
        expandItems(v) { return Array.isArray(v) ? v : [v] },
        async openEdit(r) {
            const full = await window.$api.node(r.id)
            this.dialog.isEdit = true
            this.dialog.id = r.id
            // 树节点可能缺 type（tree API 已带, 兜底当前类型）
            this.dialog.def = this.typeDefs[r.type || this.query.type] || null
            this.dialog.form = {
                slug: full.slug || '',
                status: full.status,
                sort: full.sort || 0,
                fields: full.fields || {},
            }
            // 引用回显: expand *（全部出边字段）→ refPreset（已选值显示标题, 非裸 id）
            this.dialog.form.refPreset = {}
            try {
                const ex = await window.$api.get('/admin/expand', { node: r.id, expr: '*' })
                const expand = (ex.node && ex.node.expand) || {}
                const preset = {}
                Object.keys(expand).forEach(f => {
                    const v = expand[f]
                    const items = Array.isArray(v) ? v : (v ? [v] : [])
                    preset[f] = items.map(n => ({ id: n.id, label: window.$api.refLabel(n, this.typeDefs[n.type] || null) + ' #' + n.id }))
                })
                this.dialog.form.refPreset = preset
            } catch (_) {}
            this.dialog.visible = true
        },
        async save() {
            this.dialog.saving = true
            try {
                const body = {
                    slug: this.dialog.form.slug,
                    status: this.dialog.form.status,
                    sort: this.dialog.form.sort,
                    fields: this.dialog.form.fields || {},
                }
                if (this.dialog.isEdit) {
                    await window.$api.updateNode(this.dialog.id, body)
                } else {
                    await window.$api.createNode(this.query.type, body)
                }
                ElMessage.success('已保存')
                this.dialog.visible = false
                this.refresh()
            } catch (_) { /* api.js 已提示 */ }
            this.dialog.saving = false
        },
        async doDelete(r) {
            await ElMessageBox.confirm('删除 #' + r.id + ' ?（关联引用一并清理）', '确认', { type: 'warning' })
            await window.$api.deleteNode(r.id)
            ElMessage.success('已删除')
            this.refresh()
        },
        fmt(s) { return s ? s.replace('T', ' ').slice(0, 16) : '' },
        // 列表标题: 统一走 $api.refLabel（title 列 → slug → 类型字段序兜底 → expand 合成）
        titleOf(r) {
            return window.$api.refLabel(r, this.typeDefs[r.type] || null)
        },
    },
}

function emptyForm() {
    return { slug: '', status: 1, sort: 0, fields: {}, refPreset: {} }
}
</script>

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
