<template>
    <el-dialog append-to-body v-model="visibleModel" :title="isEdit ? '编辑 #' + node.id : '新建 ' + (typeName || '')"
               width="80vw">
        <el-form>
            <el-form-item label="slug">
                <el-input v-model="form.slug" placeholder="URL 段（留空 = /node/{id}）" />
            </el-form-item>
            <el-form-item label="状态">
                <el-radio-group v-model="form.status">
                    <el-radio :value="1">已发布</el-radio>
                    <el-radio :value="0">草稿</el-radio>
                </el-radio-group>
            </el-form-item>
            <el-form-item label="排序">
                <el-input-number v-model="form.sort" :min="0" />
            </el-form-item>
            <el-divider style="margin:8px 0 16px;" />
            <field-renderer v-if="def" :fields="def.fields" v-model="form.fields"
                            :ref-preset="form.refPreset || {}" :defs="defs" />
        </el-form>
        <template #footer>
            <el-button @click="visibleModel = false">取消</el-button>
            <el-button type="primary" :loading="saving" @click="save">保存</el-button>
        </template>
    </el-dialog>
</template>
<script>
// NodeEditDialog: 节点新建/编辑共用表单。
// v-model:visible 控制显隐; @changed 保存成功后通知。
export default {
    name: 'NodeEditDialog',
    components: { FieldRenderer: Vue.defineAsyncComponent(() => window.Panel.loadComponent('pages/FieldRenderer.vue')) },
    props: {
        visible: { type: Boolean, required: true },
        node: { type: Object, default: () => ({}) },   // 编辑: 行数据（含 id/type）
        defs: { type: Object, default: () => ({}) },
        typeName: { type: String, default: '' },       // 新建类型（编辑用 node.type）
        parentId: { type: Number, default: 0 },        // 新建子的父 id
        isEdit: { type: Boolean, default: false },
    },
    emits: ['update:visible', 'changed'],
    data() {
        return { form: { slug: '', status: 1, sort: 0, fields: {}, refPreset: {} }, saving: false, def: null }
    },
    computed: {
        // v-model:visible 代理 — prop 只读, 内部写走 emit
        visibleModel: {
            get() { return this.visible },
            set(v) { this.$emit('update:visible', v) },
        },
    },
    watch: {
        visible(v) {
            if (!v) return
            this.saving = false
            if (this.isEdit) this.loadEdit()
            else this.loadCreate()
        },
    },
    methods: {
        loadCreate() {
            this.def = this.defs[this.typeName] || null
            this.form = { slug: '', status: 1, sort: 0, fields: {}, refPreset: {} }
            if (this.parentId) this.form.fields.parent = this.parentId
        },
        loadEdit() {
            var r = this.node
            var type = r.type || this.typeName
            this.def = this.defs[type] || null
            window.$api.node(r.id).then((full) => {
                this.form = {
                    slug: full.slug || '',
                    status: full.status,
                    sort: full.sort || 0,
                    fields: full.fields || {},
                    refPreset: {},
                }
                // 引用回显: expand * → refPreset（已选值显示标题, 非裸 id）
                window.$api.get('/admin/expand', { node: r.id, expr: '*' }).then((ex) => {
                    var expand = (ex.node && ex.node.expand) || {}
                    var preset = {}
                    Object.keys(expand).forEach((f) => {
                        var v = expand[f]
                        var items = Array.isArray(v) ? v : (v ? [v] : [])
                        preset[f] = items.map((n) => ({ id: n.id,
                            label: window.$api.refLabel(n, this.defs[n.type] || null) + ' #' + n.id }))
                    })
                    this.form.refPreset = preset
                }).catch(() => {})
            }).catch(() => {})
        },
        save() {
            this.saving = true
            var body = {
                slug: this.form.slug,
                status: this.form.status,
                sort: this.form.sort,
                fields: this.form.fields || {},
            }
            var p = this.isEdit
                ? window.$api.updateNode(this.node.id, body)
                : window.$api.createNode(this.typeName, body)
            p.then(() => {
                ElMessage.success('已保存')
                this.$emit('changed')
                this.visibleModel = false
            }).catch(() => {}).finally(() => { this.saving = false })
        },
    },
}
</script>
