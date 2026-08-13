<template>
  <el-color-picker :model-value="modelValue" @update:model-value="$emit('update:modelValue', $event)" />
</template>
<script>
// 站点扩展控件（color kind 的编辑原语）: /admin/ui-extras/color.vue 动态加载。
// 契约: modelValue 输入 / update:modelValue 输出 — FieldRenderer 统一绑定。
export default {
    name: 'ColorWidget',
    props: { modelValue: { type: String, default: '' } },
    emits: ['update:modelValue'],
}
</script>
