<script setup lang="ts">
import { date } from '../lib/settings'
defineProps<{ loading?: boolean; error?: Error; updatedAt?: string }>()
defineEmits<{ retry: [] }>()
</script>
<template>
  <div v-if="error" class="notice danger" role="alert"><strong>读取失败／数据可能已过时</strong><p>{{ error.message }}</p><button type="button" :disabled="loading" @click="$emit('retry')">重新读取</button></div>
  <p v-if="updatedAt" class="muted small" :aria-busy="loading || undefined" :data-resource-loading="loading ? '' : undefined">最后读取：{{ date(updatedAt) }} <slot /><span v-if="loading" class="sr-only" role="status">正在更新服务端状态…</span></p>
  <p v-else-if="loading" class="muted small" role="status" data-resource-loading>正在读取服务端状态…</p>
</template>
