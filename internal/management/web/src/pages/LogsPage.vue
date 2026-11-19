<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { query, request } from '../api/client'
import type { OperationLog } from '../api/types'
import { useResource } from '../lib/useResource'
import { logQuery } from '../lib/logs'
import { label } from '../lib/domain'
import { date, systemTimezone } from '../lib/settings'
import ResourceState from '../components/ResourceState.vue'
import PagerControl from '../components/PagerControl.vue'
const route = useRoute()
const router = useRouter()
function routeFilters() { return { action: String(route.query.action || ''), object_id: String(route.query.object_id || ''), result: String(route.query.result || ''), start: String(route.query.start || ''), end: String(route.query.end || '') } }
const filters = ref(routeFilters())
const applied = ref({ ...filters.value })
const page = ref(1)
const resource = useResource(() => request<OperationLog[]>('/operation-logs' + query(logQuery(applied.value, systemTimezone.value))), { key: () => JSON.stringify(applied.value) + systemTimezone.value })
const resultOptions = [
  { value: 'accepted', text: '已受理' }, { value: 'completed', text: '已完成' }, { value: 'success', text: '成功' },
  { value: 'rejected', text: '已拒绝' },
  { value: 'failed', text: '失败' }, { value: 'unconfirmed', text: '结果未确认' }, { value: 'canceled', text: '已取消' },
  { value: 'cancelled', text: '已取消（cancelled）' },
  { value: 'no_results', text: '无合格结果' }, { value: 'interrupted', text: '已中断' }, { value: 'skipped', text: '已跳过' },
  { value: 'cleared', text: '已解除' }, { value: 'recycled', text: '已回收' }, { value: 'removed', text: '已清理' },
]
watch(() => route.query, () => { filters.value = routeFilters(); applied.value = { ...filters.value }; page.value = 1 })
function apply() { applied.value = { ...filters.value }; page.value = 1; void router.replace({ query: Object.fromEntries(Object.entries(applied.value).filter(([, value]) => value)) }) }
</script>
<template><header class="page-heading"><div><h1 tabindex="-1">操作日志</h1><p class="muted">共享只读，固定保留 30 天。只记录关键动作；主体为共享管理身份或系统，不提供逐条改删。</p></div></header><form class="filter-panel" @submit.prevent="apply"><p id="filter-timezone" class="small muted">时间显示与筛选均按系统时区 {{ systemTimezone || '读取中' }}。夏令时跳变或重复时刻请调整筛选边界。</p><div class="form-grid"><label>动作<input v-model.trim="filters.action" /></label><label>对象 / 原尝试 ID<input v-model.trim="filters.object_id" /></label><label>结果<select v-model="filters.result" aria-label="结果"><option value="">全部结果</option><option v-for="option in resultOptions" :key="option.value" :value="option.value">{{ option.text }}</option></select></label><label>开始时间<input v-model="filters.start" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label><label>结束时间<input v-model="filters.end" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label></div><div class="actions"><button class="primary">查询日志</button><button type="button" @click="filters = { action: '', object_id: '', result: '', start: '', end: '' }; apply()">清除条件</button></div></form><ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><table v-if="resource.data.value?.length" class="responsive-table"><caption class="sr-only">脱敏操作日志</caption><thead><tr><th>时间 / 主体</th><th>动作 / 对象</th><th>结果 / 原因</th></tr></thead><tbody><tr v-for="(log, index) in resource.data.value.slice((page - 1) * 20, page * 20)" :key="log.id || index"><td data-label="时间 / 主体">{{ date(log.created_at || log.at) }}<span class="block">{{ log.actor || '未知身份' }}</span></td><td data-label="动作 / 对象">{{ log.action || '未知动作' }}<code class="block">{{ log.object_id || log.object || '未知对象' }}</code><code v-if="log.operation_id" class="block">操作号 {{ log.operation_id }}</code></td><td data-label="结果 / 原因">{{ label(log.state || log.result) }}<span class="block">{{ log.message || log.reason || '未提供原因' }}</span></td></tr></tbody></table><p v-else-if="resource.data.value && !resource.error.value" class="empty-state">当前条件没有日志。日志没有回收站，不支持逐条删除。</p><PagerControl v-if="resource.data.value" :page="page" :size="20" :total="resource.data.value.length" @change="page = $event" /></template>
