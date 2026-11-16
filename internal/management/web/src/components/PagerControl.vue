<script setup lang="ts">
defineProps<{ page: number; total: number; size: number }>()
defineEmits<{ change: [number] }>()
</script>
<template><nav class="pagination" aria-label="分页"><button :disabled="page <= 1" @click="$emit('change', page - 1)">上一页</button><span>第 {{ page }} / {{ Math.max(1, Math.ceil(total / size)) }} 页 · {{ total }} 条</span><button :disabled="page * size >= total" @click="$emit('change', page + 1)">下一页</button></nav></template>
