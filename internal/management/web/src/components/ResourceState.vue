<script setup lang="ts">
import { date } from '../lib/settings'
defineProps<{ loading?: boolean; error?: Error; updatedAt?: string }>()
defineEmits<{ retry: [] }>()
</script>
<template>
  <div v-if="error" class="notice danger" role="alert"><strong>读取失败／数据可能已过时</strong><p>{{ error.message }}</p><button type="button" :disabled="loading" @click="$emit('retry')">重新读取</button></div>
  <p v-if="loading" class="muted" role="status" data-resource-loading>正在读取服务端状态…</p>
  <p v-else-if="updatedAt" class="muted small">最后读取：{{ date(updatedAt) }} <slot /></p>
</template>
