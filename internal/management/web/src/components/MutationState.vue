<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import type { ApiError } from '../api/client'
import type { Receipt } from '../api/types'
import StatusBadge from './StatusBadge.vue'
const props = defineProps<{ error?: ApiError; receipt?: Receipt }>()
const feedback = ref<HTMLElement>()
watch(() => props.error, async value => { if (value) { await nextTick(); feedback.value?.focus() } })
</script>
<template>
  <div v-if="error" ref="feedback" class="notice danger" role="alert" tabindex="-1">
    <strong>{{ error.message }}</strong><p v-if="error.details.code">错误码：<code>{{ error.details.code }}</code></p>
    <p v-if="error.details.operation_id">原操作号：<code>{{ error.details.operation_id }}</code>。系统已自动查询上次结果；仍未确认时请核对本页数据，不要重复提交。</p>
    <ul v-if="error.details.fields"><li v-for="(value, key) in error.details.fields" :key="key">{{ key }}：{{ Array.isArray(value) ? value.join('；') : value }}</li></ul>
  </div>
  <div v-if="receipt" class="notice" role="status">
    <strong>已收到操作收据</strong> <StatusBadge :value="receipt.state" />
    <p>操作号：<code>{{ receipt.operation_id }}</code><template v-if="receipt.object_id"> · 对象：<code>{{ receipt.object_id }}</code></template></p>
    <p class="small">受理不等于运行结束或外部送达；请查看对象的实际状态。结果未确认时先核对本页数据，不要重复提交。</p>
  </div>
</template>
