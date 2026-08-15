<template>
    <div>
        <div style="display:flex;gap:16px;align-items:flex-start;">
            <!-- 树: 该类型全量 -->
            <div class="nodes-tree" style="width:220px;">
                <div class="nodes-tree-header">
                    <span style="font-weight:600;font-size:14px;">{{ def?.title || typeName }}树</span>
                    <el-button link type="primary" size="small" @click="loadTree">刷新</el-button>
                </div>
                <div style="max-height:70vh;overflow:auto;">
                    <el-tree :data="treeNodes" node-key="id" default-expand-all
                             :expand-on-click-node="false" highlight-current
                             :current-node-key="activeId" @node-click="onNodeClick">
                        <template #default="{ data }">
                            <span class="tree-node-label" style="font-size:13px;">{{ titleOf(data) }}</span>
                        </template>
                    </el-tree>
                    <div v-if="!treeNodes.length" style="color:#9ca3af;padding:20px;text-align:center;">空</div>
                </div>
            </div>

            <!-- in 方向列表: 选中分支的子树被哪些节点引用（无论类型 + 溯源） -->
            <div style="flex:1;min-width:0;">
                <div style="display:flex;align-items:center;gap:8px;margin-bottom:8px;">
                    <span style="font-weight:600;">引用列表</span>
                    <el-tag v-if="activeNode" size="small">{{ activeNode.title || '#' + activeNode.id }}</el-tag>
                    <el-checkbox v-model="subtree" :disabled="!activeId" @change="loadInbound">
                        含子树
                    </el-checkbox>
                    <span v-if="total" style="color:#9ca3af;font-size:12px;">共 {{ total }} 条</span>
                </div>
                <el-table :data="rows" v-loading="loading" border stripe size="small" max-height="65vh">
                    <el-table-column prop="id" label="ID" width="70" />
                    <el-table-column label="类型" width="110">
                        <template #default="{ row }">
                            <el-tag size="small">{{ row.type }}</el-tag>
                        </template>
                    </el-table-column>
                    <el-table-column label="标题" min-width="180">
                        <template #default="{ row }">{{ row.title || row.slug || '#' + row.id }}</template>
                    </el-table-column>
                    <el-table-column label="溯源" min-width="120">
                        <template #default="{ row }">
                            <code style="font-size:12px;">{{ row.via_field }}</code>
                        </template>
                    </el-table-column>
                    <el-table-column prop="slug" label="slug" min-width="120" />
                </el-table>
                <div style="display:flex;justify-content:flex-end;margin-top:12px;">
                    <el-pagination background layout="prev, pager, next, total" :total="total"
                        :page-size="size" :current-page="page" @current-change="onPage" />
                </div>
            </div>
        </div>
    </div>
</template>
<script>
import { useRoute } from 'vue-router'
export default {
    setup() {
        var route = useRoute()
        return { typeName: route.params.type }
    },
    data() {
        return {
            def: null,
            treeNodes: [],
            activeId: 0,
            activeNode: null,
            subtree: true,
            rows: [],
            total: 0,
            page: 1,
            size: 20,
            loading: false,
        }
    },
    mounted() { this.loadTypes() },
    methods: {
        titleOf(n) {
            return n.title || n.slug || '#' + n.id
        },
        async loadTypes() {
            var res = await $api.types()
            this.def = (res.types || {})[this.typeName] || null
            this.loadTree()
        },
        async loadTree() {
            try {
                var res = await $api.get('/admin/tree', { type: this.typeName })
                this.treeNodes = this.buildTree(res.items || [], 'parent')
            } catch (_) {}
        },
        buildTree(items, parentField) {
            var map = {}
            items.forEach(function (n) { map[n.id] = { ...n, children: [] } })
            var roots = []
            items.forEach(function (n) {
                var node = map[n.id]
                var pid = n.fields && n.fields[parentField]
                var parent = pid && map[pid]
                if (parent) parent.children.push(node)
                else roots.push(node)
            })
            return roots
        },
        collectSubtree(n) {
            var ids = [n.id]
            var walk = (node) => { (node.children || []).forEach(function (c) { ids.push(c.id); walk(c) }) }
            walk(n)
            return ids
        },
        onNodeClick(n) {
            this.activeId = n.id
            this.activeNode = n
            this.page = 1
            this.loadInbound()
        },
        async loadInbound() {
            if (!this.activeId) return
            this.loading = true
            try {
                var params = { node: this.activeId, page: this.page, size: this.size }
                if (this.subtree) params.subtree = 1
                var res = await $api.get('/admin/inbound', params)
                this.rows = res.items || []
                this.total = res.total || 0
            } catch (_) { this.rows = []; this.total = 0 }
            this.loading = false
        },
        onPage(p) { this.page = p; this.loadInbound() },
    },
}
</script>
