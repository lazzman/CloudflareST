<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { request, query, segment } from '../api/client'
import type { Page, Run, Task } from '../api/types'
import { useResource } from '../lib/useResource'
import { timeQuery } from '../lib/serialize'
import { active, ADDRESS_FAMILIES, deliverySummary, duration, familyResult, familyResultText, label, metric, restoreDeadline, RUN_STATUSES } from '../lib/domain'
import { date, systemTimezone } from '../lib/settings'
import { useMutation } from '../lib/useMutation'
import ResourceState from '../components/ResourceState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import MutationState from '../components/MutationState.vue'
import ModalDialog from '../components/ModalDialog.vue'
import PagerControl from '../components/PagerControl.vue'
const route = useRoute()
const router = useRouter()
const taskId = String(route.params.taskId)
const recycled = computed(() => route.path.endsWith('/recycle-bin'))
const taskResource = useResource(() => request<Task>('/tasks/' + segment(taskId)))
const filters = ref({ start: '', end: '', status: '', trigger: '', dns_state: '', notification_state: '' })
watch(() => route.query, value => {
  for (const key of Object.keys(filters.value) as (keyof typeof filters.value)[]) filters.value[key] = typeof value[key] === 'string' ? String(value[key]) : ''
}, { immediate: true })
const size = 20
const page = computed({get: () => Math.max(1, Number.isSafeInteger(Number(route.query.page)) ? Number(route.query.page) : 1), set: value => { void router.replace({query: {...route.query, page: String(value)}}) }})
const pageNotice = ref('')
const resource = useResource(async () => {
  const saved = Object.fromEntries(['start', 'end', 'status', 'trigger', 'dns_state', 'notification_state'].map(key => [key, typeof route.query[key] === 'string' ? String(route.query[key]) : '']))
  const value = await request<Page<Run>>('/tasks/' + segment(taskId) + (recycled.value ? '/recycle-bin' : '/runs') + query({...timeQuery(saved, systemTimezone.value), page: page.value, page_size: size}))
  return value
}, {key: () => route.fullPath + systemTimezone.value, poll: value => !recycled.value && !!value?.items.some(active)})
watch(() => resource.data.value?.page, value => { if (value && value !== page.value) { pageNotice.value = '记录数量已变化，已返回有效页码。'; page.value = value } })
const mutation = useMutation()
const confirming = ref<Run>()
const visible = computed(() => resource.data.value?.items || [])
async function applyFilters() { pageNotice.value = ''; await router.replace({ query: {...Object.fromEntries(Object.entries(filters.value).filter(([, value]) => value !== '')), page: '1'} }) }
function detailLink(run: Run) {
  const saved = Object.fromEntries(Object.entries(route.query).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))
  return {path: '/tasks/' + taskId + '/runs/' + run.id, query: {list: new URLSearchParams({...saved, page: String(page.value)}).toString()}}
}
async function confirmDelete() {
  const selected = confirming.value
  if (!selected) return
  const receipt = await mutation.submit('/runs/' + segment(selected.id), { method: 'DELETE' })
  if (receipt) { confirming.value = undefined; void resource.reload() }
}
async function restore(run: Run) { if (await mutation.submit('/runs/' + segment(run.id) + '/restore')) void resource.reload() }
</script>
<template>
  <RouterLink :to="'/tasks/' + taskId">← {{ taskResource.data.value?.name || taskId }} · 任务概览</RouterLink>
  <header class="page-heading"><div><p class="eyebrow">{{ recycled ? '回收站 · 恢复不重跑、不重推送' : '仅当前任务 · 入队时间范围' }}</p><h1 tabindex="-1">{{ recycled ? '已删除运行记录' : '运行记录' }}</h1><code>{{ taskId }}</code></div><RouterLink v-if="!recycled" :to="'/tasks/' + taskId + '/recycle-bin'">回收站</RouterLink><RouterLink v-else :to="'/tasks/' + taskId + '/runs'">返回运行记录</RouterLink></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" />
  <form v-if="!recycled" class="filter-panel" @submit.prevent="applyFilters"><p id="filter-timezone" class="small muted">时间显示与筛选均按系统时区 {{ systemTimezone || '读取中' }}。夏令时跳变或重复时刻请调整筛选边界。</p><div class="form-grid"><label>入队开始时间<input v-model="filters.start" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label><label>入队结束时间<input v-model="filters.end" type="datetime-local" :disabled="!systemTimezone" aria-describedby="filter-timezone" /></label><label>主状态<select v-model="filters.status" aria-label="主状态"><option value="">所有状态</option><option v-for="value in RUN_STATUSES" :key="value" :value="value">{{ label(value) }}</option></select></label><label>触发来源<select v-model="filters.trigger"><option value="">所有触发</option><option value="manual">手动</option><option value="scheduled">定时</option></select></label><label>DNS 状态<select v-model="filters.dns_state"><option value="">所有</option><option v-for="value in ['success','failed','skipped','unconfirmed']" :key="value" :value="value">{{ label(value) }}</option></select></label><label>通知状态<select v-model="filters.notification_state"><option value="">所有</option><option v-for="value in ['success','failed','skipped','unconfirmed']" :key="value" :value="value">{{ label(value) }}</option></select></label></div><div class="actions"><button class="primary">应用筛选</button><button type="button" @click="filters = { start: '', end: '', status: '', trigger: '', dns_state: '', notification_state: '' }; void applyFilters()">清除筛选</button></div></form>
  <MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <p v-if="pageNotice" class="notice" role="status">{{ pageNotice }}</p>
  <table v-if="resource.data.value && visible.length" class="responsive-table"><caption class="sr-only">{{ recycled ? '待恢复' : '执行' }}记录及 DNS／通知独立投递结果</caption><thead><tr><th scope="col">执行 / 时间 / 耗时</th><th scope="col">主状态 / 当前阶段 / 席位</th><th scope="col">触发 / 各族合格数</th><th scope="col">各族第一名（实际指标）</th><th scope="col">DNS / Telegram / 企业微信</th><th scope="col">操作</th></tr></thead><tbody><tr v-for="run in visible" :key="run.id">
    <td data-label="执行 / 时间 / 耗时"><RouterLink v-if="!recycled" :to="detailLink(run)"><code>{{ run.id }}</code></RouterLink><code v-else>{{ run.id }}</code><span class="block small">入队 {{ date(run.queued_at) }}</span><span class="block small">实际开始 {{ date(run.started_at) }}</span><span class="block small">收尾结束 {{ date(run.ended_at) }}</span><span class="block small">耗时 {{ duration(run) }}</span></td>
    <td data-label="主状态 / 阶段 / 席位"><StatusBadge :value="run.status" /> · {{ label(run.stage) }}<span class="block small">{{ run.slot_busy === undefined ? '席位未知' : run.slot_busy ? '席位占用（含正常收尾）' : '未占用' }}</span></td>
    <td data-label="触发 / 各族合格数">{{ label(run.trigger) }}<span v-for="addressFamily in ADDRESS_FAMILIES" :key="addressFamily" class="block">{{ addressFamily === 'ipv4' ? 'IPv4' : 'IPv6' }} {{ familyResultText(familyResult(run, addressFamily)) }}</span></td>
    <td data-label="各族第一名 / 实测"><div v-for="addressFamily in ADDRESS_FAMILIES" :key="addressFamily" class="block"><strong>{{ addressFamily === 'ipv4' ? 'IPv4' : 'IPv6' }}</strong><template v-for="result in [familyResult(run, addressFamily)]" :key="result.family"><template v-if="result.best"><code class="block">{{ result.best.ip }}</code><span class="block small">延迟 {{ metric(result.best.latency_ms, ' ms') }} / 失败比例 {{ metric(result.best.loss_percent, '%') }} / 下载 {{ metric(result.best.speed_mb_s, ' MB/s') }}</span></template><span v-else class="block small">{{ result.state === 'ready' ? '最终第一名未知' : familyResultText(result) }}</span></template></div></td>
    <td data-label="DNS / 各通知"><span class="block">DNS：{{ deliverySummary(run, 'dns') }}</span><span class="block small">Telegram：{{ deliverySummary(run, 'telegram') }}</span><span class="block small">企业微信：{{ deliverySummary(run, 'wecom') }}</span></td>
    <td data-label="操作"><template v-if="recycled"><p class="small">删除于 {{ date(run.deleted_at) }} · 恢复期至 {{ date(restoreDeadline(run)) }}（删除后 7 天）</p><button type="button" :disabled="mutation.busy.value" @click="restore(run)">恢复记录</button></template><template v-else><RouterLink :to="detailLink(run)">查看详情</RouterLink><p v-if="active(run)" class="small muted">活动执行受保护，不能移入回收站</p><button v-else type="button" @click="confirming = run">移入 7 天回收站</button></template></td>
  </tr></tbody></table>
  <section v-else-if="resource.data.value && !resource.error.value" class="empty-state"><h2>{{ recycled ? '回收站为空' : '此条件下没有运行记录' }}</h2><p>{{ recycled ? '被删除记录保留 7 天；恢复后重新起算运行历史保留期，不修改原执行时间。' : '筛选只查询当前任务入队记录；可清除筛选，不会混入其他任务。' }}</p></section>
  <PagerControl v-if="resource.data.value" :page="page" :total="resource.data.value.total" :size="size" @change="page = $event" />
  <ModalDialog v-if="confirming" title="将结束记录移入 7 天回收站" @close="confirming = undefined"><p>任务：{{ confirming.task_name || taskResource.data.value?.name || taskId }}</p><p>执行：<code>{{ confirming.id }}</code></p><p>运行时间：{{ date(confirming.started_at) }} → {{ date(confirming.ended_at) }}</p><p class="notice warning">只删除本站可恢复的运行历史，不撤销 DNS 写入或消息；若投递仍在收尾，服务端会拒绝。</p><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" /><div class="actions"><button class="safe-focus" @click="confirming = undefined">保留记录</button><button class="danger-button" :disabled="mutation.busy.value" @click="confirmDelete">移入回收站</button></div></ModalDialog>
  <p v-if="pageNotice" class="notice" role="status">{{ pageNotice }}</p>
</template>
