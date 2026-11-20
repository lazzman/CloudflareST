<script setup lang="ts">
import { computed } from 'vue'
const props = withDefaults(defineProps<{ page: number; total: number; size: number; showCount?: boolean }>(), { showCount: true })
defineEmits<{ change: [number] }>()
const pages = computed(() => Math.max(1, Math.ceil(props.total / props.size)))
</script>
<template>
  <nav v-if="total > size" class="pagination" aria-label="分页"><button :disabled="page <= 1" @click="$emit('change', page - 1)">上一页</button><span>第 {{ page }} / {{ pages }} 页 · {{ total }} 条</span><button :disabled="page * size >= total" @click="$emit('change', page + 1)">下一页</button></nav>
  <p v-else-if="showCount && total > 0" class="pagination-count small muted">共 {{ total }} 条</p>
</template>
