<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { query, request } from '../api/client'
import type { OperationLog } from '../api/types'
import { useResource } from '../lib/useResource'
import { ACTION_GROUPS, RESULT_GROUPS, actionFilterValues, actionText, logQuery } from '../lib/logs'
import { label } from '../lib/domain'
import { date, systemTimezone } from '../lib/settings'
import { instantToSystemInput } from '../lib/datetime'
import ResourceState from '../components/ResourceState.vue'
import PagerControl from '../components/PagerControl.vue'
const route = useRoute()
const router = useRouter()
function routeFilters() { return { action: String(route.query.action || ''), actor: String(route.query.actor || ''), object_id: String(route.query.object_id || ''), result: String(route.query.result || ''), start: String(route.query.start || ''), end: String(route.query.end || '') } }
const filters = ref(routeFilters())
const applied = ref({ ...filters.value })
const page = ref(1)
const resource = useResource(() => request<OperationLog[]>('/operation-logs' + query(logQuery(applied.value, systemTimezone.value))), { key: () => JSON.stringify(applied.value) + systemTimezone.value })
const actorOptions = [
  { value: '', text: '全部操作人' },
  { value: '共享管理身份', text: '共享管理身份' },
  { value: '系统', text: '系统' },
]
const actionGroups = computed(() => {
  const value = filters.value.action
  if (!value || actionFilterValues.has(value)) return ACTION_GROUPS
  return [...ACTION_GROUPS, { label: '旧链接条件', options: [{ value, text: '自定义：' + value }] }]
})
watch(() => route.query, () => { filters.value = routeFilters(); applied.value = { ...filters.value }; page.value = 1 })
function apply() { applied.value = { ...filters.value }; page.value = 1; void router.replace({ query: Object.fromEntries(Object.entries(applied.value).filter(([, value]) => value)) }) }
function setRange(hours: number) {
  if (!systemTimezone.value) return
  const now = Date.now()
  filters.value.end = instantToSystemInput(now, systemTimezone.value)
  filters.value.start = instantToSystemInput(now - hours * 3600000, systemTimezone.value)
  apply()
}
function setToday() {
  if (!systemTimezone.value) return
  const end = instantToSystemInput(Date.now(), systemTimezone.value)
  filters.value.end = end
  filters.value.start = end.slice(0, 10) + 'T00:00'
  apply()
}
</script>
<template>
  <header class="page-heading">
    <div>
      <h1 tabindex="-1">操作日志</h1>
      <p class="muted">共享只读，固定保留 30 天。只记录关键动作；主体为共享管理身份或系统，不提供逐条改删。</p>
    </div>
  </header>
  <form class="filter-panel" @submit.prevent="apply">
    <p id="filter-timezone" class="small muted">时间显示与筛选均按系统时区 {{ systemTimezone || '读取中' }}。夏令时跳变或重复时刻请调整筛选边界。</p>
    <div class="form-grid">
      <label>动作
        <select v-model="filters.action" aria-label="动作">
          <option value="">全部动作</option>
          <optgroup v-for="group in actionGroups" :key="group.label" :label="group.label">
            <option v-for="option in group.options" :key="option.value" :value="option.value">{{ option.text }}</option>
          </optgroup>
        </select>
      </label>
      <label>操作人
        <select v-model="filters.actor" aria-label="操作人">
          <option v-for="option in actorOptions" :key="option.value" :value="option.value">{{ option.text }}</option>
        </select>
      </label>
      <label>对象 / 原尝试 ID<input v-model.trim="filters.object_id" /></label>
      <label>结果
        <select v-model="filters.result" aria-label="结果">
          <option value="">全部结果</option>
          <optgroup v-for="group in RESULT_GROUPS" :key="group.label" :label="group.label">
            <option v-for="option in group.options" :key="option.value" :value="option.value">{{ option.text }}</option>
          </optgroup>
        </select>
      </label>
      <label>开始时间<input v-model="filters.start" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label>
      <label>结束时间<input v-model="filters.end" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label>
    </div>
    <div class="actions">
      <span class="small muted">快捷时间</span>
      <button type="button" :disabled="!systemTimezone" @click="setToday">今天</button>
      <button type="button" :disabled="!systemTimezone" @click="setRange(24)">最近 24 小时</button>
      <button type="button" :disabled="!systemTimezone" @click="setRange(24 * 7)">最近 7 天</button>
      <button type="button" :disabled="!systemTimezone" @click="setRange(24 * 30)">最近 30 天</button>
    </div>
    <div class="actions">
      <button class="primary">查询日志</button>
      <button type="button" @click="filters = { action: '', actor: '', object_id: '', result: '', start: '', end: '' }; apply()">清除条件</button>
    </div>
  </form>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" />
  <table v-if="resource.data.value?.length" class="responsive-table">
    <caption class="sr-only">脱敏操作日志</caption>
    <thead><tr><th>时间 / 主体</th><th>动作 / 对象</th><th>结果 / 原因</th></tr></thead>
    <tbody>
      <tr v-for="(log, index) in resource.data.value.slice((page - 1) * 20, page * 20)" :key="log.id || index">
        <td data-label="时间 / 主体">{{ date(log.created_at || log.at) }}<span class="block">{{ log.actor || '未知身份' }}</span></td>
        <td data-label="动作 / 对象"><span :title="log.action || ''">{{ actionText(log) }}</span><code class="block">{{ log.object_id || log.object || '未知对象' }}</code><code v-if="log.operation_id" class="block">操作号 {{ log.operation_id }}</code></td>
        <td data-label="结果 / 原因">{{ label(log.state || log.result) }}<span class="block">{{ log.message || log.reason || '未提供原因' }}</span></td>
      </tr>
    </tbody>
  </table>
  <p v-else-if="resource.data.value && !resource.error.value" class="empty-state">当前条件没有日志。日志没有回收站，不支持逐条删除。</p>
  <PagerControl v-if="resource.data.value" :page="page" :size="20" :total="resource.data.value.length" @change="page = $event" />
</template>
