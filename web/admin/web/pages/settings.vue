<template>
  <div class="settings-page">
    <div style="display:flex;gap:8px;align-items:center;margin-bottom:14px;">
      <span style="font-size:16px;font-weight:600;">站点配置</span>
      <el-button size="small" @click="load">刷新</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe size="small">
      <el-table-column prop="key" label="key" min-width="140">
        <template #default="{ row: r }"><code style="font-weight:600;">{{ r.key }}</code></template>
      </el-table-column>
      <el-table-column prop="kind" label="类型" width="100">
        <template #default="{ row: r }">{{ (types[r.key] || {}).kind || '—' }}</template>
      </el-table-column>
      <el-table-column label="描述" min-width="180">
        <template #default="{ row: r }">{{ (types[r.key] || {}).note || r.note || '' }}</template>
      </el-table-column>
      <el-table-column label="值" min-width="160">
        <template #default="{ row: r }">
          <span style="color:#555;">{{ r.value === undefined || r.value === null ? '未设置' : valueOf(r) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140" fixed="right">
        <template #default="{ row: r }">
          <el-button link type="primary" size="small" @click="openEdit(r)">编辑</el-button>
          <el-button link type="danger" size="small" @click="doClear(r)">清空</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog.visible" :title="'编辑 ' + dialog.key" width="70vw">
      <div style="color:#999;font-size:12px;margin-bottom:10px;">
        {{ (types[dialog.key] || {}).note || '' }}
      </div>
      <field-renderer v-if="fieldDef" :fields="[fieldDef]" v-model="valueModel" :defs="{}" />
      <template #footer>
        <el-button @click="dialog.visible = false">取消</el-button>
        <el-button type="primary" :loading="dialog.saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
<script>
import FieldRenderer from './FieldRenderer.vue'
export default {
    name: 'SettingsPage',
    components: { FieldRenderer },
    data() {
        return {
            rows: [], types: {}, loading: false,
            dialog: { visible: false, saving: false, key: '', field: null },
            valueModel: {},
        }
    },
    computed: {
        fieldDef() {
            const f = this.dialog.field
            return f ? { name: 'value', label: this.dialog.key, ...f } : null
        },
    },
    async mounted() { await this.load() },
    methods: {
        async load() {
            this.loading = true
            try {
                const res = await window.$api.get('/admin/settings')
                this.types = res.types || {}
                // 列表 = 注册表全项（未设置也显示, 运营看到完整配置清单）
                const items = res.items || []
                const byKey = {}
                items.forEach(r => { byKey[r.key] = r })
                this.rows = Object.keys(this.types).sort().map(k => byKey[k] || { key: k })
            } finally { this.loading = false }
        },
        valueOf(r) {
            if (r.value === undefined || r.value === null) return '—'
            const s = JSON.stringify(r.value)
            return s.length > 40 ? s.slice(0, 40) + '…' : s
        },
        openEdit(r) {
            const def = this.types[r.key]
            if (!def) return
            this.dialog.key = r.key
            this.dialog.field = def
            this.valueModel = { value: r.value }
            this.dialog.visible = true
        },
        async save() {
            this.dialog.saving = true
            try {
                await window.$api.post('/admin/settings', { key: this.dialog.key, value: this.valueModel.value })
                ElMessage.success('已保存')
                this.dialog.visible = false
                this.load()
            } catch (_) { /* api.js 已提示 */ }
            this.dialog.saving = false
        },
        async doClear(r) {
            await ElMessageBox.confirm('清空配置 ' + r.key + ' ?', '确认', { type: 'warning' })
            await window.$api.post('/admin/settings', { key: r.key, value: null })
            ElMessage.success('已清空')
            this.load()
        },
    },
}
</script>
