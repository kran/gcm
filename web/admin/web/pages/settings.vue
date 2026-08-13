<template>
  <div class="settings-page">
    <div style="display:flex;gap:8px;align-items:center;margin-bottom:14px;">
      <span style="font-size:16px;font-weight:600;">站点配置</span>
      <el-select v-model="group" placeholder="分组" size="small" style="width:160px" clearable @change="load">
        <el-option v-for="g in groups" :key="g" :label="g || '(未分组)'" :value="g" />
      </el-select>
      <el-button size="small" @click="load">刷新</el-button>
      <el-button type="primary" size="small" @click="openCreate">新建配置</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" border stripe size="small">
      <el-table-column prop="key" label="key" min-width="140">
        <template #default="{ row: r }"><code style="font-weight:600;">{{ r.key }}</code></template>
      </el-table-column>
      <el-table-column prop="group" label="分组" width="110" />
      <el-table-column prop="kind" label="类型" width="90" />
      <el-table-column prop="note" label="描述" min-width="180" />
      <el-table-column label="值" min-width="160">
        <template #default="{ row: r }"><span style="color:#555;">{{ valueOf(r) }}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="140" fixed="right">
        <template #default="{ row: r }">
          <el-button link type="primary" size="small" @click="openEdit(r)">编辑</el-button>
          <el-button link type="danger" size="small" @click="doDelete(r)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog.visible" :title="dialog.isEdit ? '编辑 ' + dialog.form.key : '新建配置'" width="70vw">
      <el-form label-width="80px">
        <el-form-item label="key">
          <el-input v-model="dialog.form.key" :disabled="dialog.isEdit" placeholder="唯一标识（如 footer）" />
        </el-form-item>
        <el-form-item label="分组">
          <el-input v-model="dialog.form.group" placeholder="如 site / seo / list" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="dialog.form.kind" style="width:200px;">
            <el-option v-for="k in kindNames" :key="k" :label="k" :value="k" />
          </el-select>
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="dialog.form.note" placeholder="这个配置是干什么的（时间长了不认识的解药）" />
        </el-form-item>
        <el-form-item label="值">
          <field-renderer :fields="valueField" v-model="valueModel" :defs="{}" />
        </el-form-item>
      </el-form>
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
            rows: [], groups: [], group: '', loading: false,
            kindNames: ['string', 'text', 'richtext', 'number', 'bool', 'array', 'object'],
            dialog: { visible: false, isEdit: false, saving: false, form: {} },
            valueModel: {},
        }
    },
    computed: {
        // 值编辑: 伪装成单字段 FieldDef（kind → editor 映射自动生效, 复合字段也支持）
        valueField() {
            const kind = this.dialog.form.kind || 'string'
            return [{ name: 'value', kind: kind, label: '值' }]
        },
    },
    async mounted() { await this.load() },
    methods: {
        async load() {
            this.loading = true
            try {
                const res = await window.$api.get('/admin/settings', { group: this.group })
                this.rows = res.items || []
                const gs = {}
                this.rows.forEach(r => { if (!gs[r.group]) gs[r.group] = true })
                this.groups = Object.keys(gs).sort()
            } finally { this.loading = false }
        },
        valueOf(r) {
            if (r.value === null || r.value === undefined) return '—'
            const s = JSON.stringify(r.value)
            return s.length > 40 ? s.slice(0, 40) + '…' : s
        },
        openCreate() {
            this.dialog.isEdit = false
            this.dialog.form = { key: '', group: '', kind: 'string', note: '' }
            this.valueModel = { value: '' }
            this.dialog.visible = true
        },
        openEdit(r) {
            this.dialog.isEdit = true
            this.dialog.form = { key: r.key, group: r.group, kind: r.kind, note: r.note }
            this.valueModel = { value: r.value }
            this.dialog.visible = true
        },
        async save() {
            this.dialog.saving = true
            try {
                await window.$api.post('/admin/settings', {
                    key: this.dialog.form.key,
                    group: this.dialog.form.group,
                    kind: this.dialog.form.kind,
                    note: this.dialog.form.note,
                    value: this.valueModel.value,
                })
                ElMessage.success('已保存')
                this.dialog.visible = false
                this.load()
            } catch (_) { /* api.js 已提示 */ }
            this.dialog.saving = false
        },
        async doDelete(r) {
            await ElMessageBox.confirm('删除配置 ' + r.key + ' ?', '确认', { type: 'warning' })
            // 删除 = 值清空 upsert 空? — settings 无删除 API, 用空值覆盖（语义: 回到默认）
            await window.$api.post('/admin/settings', { key: r.key, group: r.group, kind: r.kind, note: r.note, value: '' })
            ElMessage.success('已清空')
            this.load()
        },
    },
}
</script>
