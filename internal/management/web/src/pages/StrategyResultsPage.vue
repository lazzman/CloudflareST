<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { request, query, segment } from '../api/client'
import type { Candidate, Page, Receipt, Run, Task } from '../api/types'
import { useResource } from '../lib/useResource'
import { useMutation } from '../lib/useMutation'
import { active, metric, qualifiedCount } from '../lib/domain'
import { resultColumns, type ResultSort, type ResultDirection } from '../lib/resultView'
import { date } from '../lib/settings'
import ResourceState from '../components/ResourceState.vue'
import MutationState from '../components/MutationState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import PagerControl from '../components/PagerControl.vue'
import ModalDialog from '../components/ModalDialog.vue'
import DnsDeliveryDialog from '../components/DnsDeliveryDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import PendingOperationNotice from '../components/PendingOperationNotice.vue'
const route = useRoute(), router = useRouter()
const taskId = String(route.params.taskId), base = '/tasks/' + segment(taskId)
const historyPage = ref(1), latestId = ref('')
const taskResource = useResource(() => request<Task>(base))
const history = useResource(() => request<Page<Run>>(base + '/runs' + query({page: historyPage.value, page_size: 20})), {key: () => historyPage.value, poll: () => historyPage.value === 1})
watch(() => history.data.value, value => {
  if (!value) return
  if (value.page === 1) latestId.value = value.items[0]?.id || ''
  if (value.page !== historyPage.value) historyPage.value = value.page
})
const requestedRun = computed(() => typeof route.query.run === 'string' ? route.query.run : '')
const selectedId = computed(() => requestedRun.value || latestId.value)
const resource = useResource(() => selectedId.value ? request<Run>(base + '/runs/' + segment(selectedId.value) + '?view=summary') : Promise.resolve(undefined), {key: () => selectedId.value, poll: value => active(value) || !!value?.deliveries?.some(item => ['pending','dispatching','running','accepted'].includes(item.state || ''))})
const run = computed(() => resource.data.value)
const page = ref(1), family = ref(''), eligibility = ref('eligible')
const sort = ref<ResultSort>('latency_ms'), direction = ref<ResultDirection>('asc')
const candidates = useResource(() => run.value ? request<Page<Candidate>>(base + '/runs/' + segment(run.value.id) + '/candidates' + query({page: page.value, page_size: 50, family: family.value, eligibility: eligibility.value, sort: sort.value, direction: direction.value})) : Promise.resolve(undefined), {key: () => [run.value?.id, run.value?.result?.status, run.value?.candidate_summary?.total, page.value, family.value, eligibility.value, sort.value, direction.value].join(':')})
watch(selectedId, () => { page.value = 1 })
watch(() => candidates.data.value?.page, value => { if (value && value !== page.value) page.value = value })
const deliveryWarning = computed(() => run.value?.deliveries?.some(item => ['failed','unconfirmed','unknown'].includes(item.state || '')))
const runError = computed(() => run.value?.reason || run.value?.result?.error)
const mutation = useMutation(), stopping = ref(false)
const dnsOpen = ref(false)
const dnsReceipt = ref<Receipt>()
const dnsQualified = computed(() => {
  const current = run.value
  if (!current) return undefined
  const ipv4 = qualifiedCount(current, 'ipv4')
  const ipv6 = qualifiedCount(current, 'ipv6')
  return ipv4 === undefined && ipv6 === undefined ? undefined : (ipv4 || 0) + (ipv6 || 0)
})
const dnsReady = computed(() => {
  const current = run.value
  if (!current || active(current) || current.deleted_at) return false
  return (dnsQualified.value || 0) > 0
})
const dnsBlockReason = computed(() => {
  const current = run.value
  if (!current) return '请先选择一次运行'
  if (active(current)) return '本次执行结束后才能手动推送'
  if (current.deleted_at) return '已删除的运行不能推送'
  if (dnsQualified.value === undefined) return '本次合格 IP 未知，暂不能手动推送'
  return '本次没有可推送的最终合格 IP'
})
function openDns() { dnsReceipt.value = undefined; dnsOpen.value = true }
function dnsDone(receipt: Receipt) { dnsReceipt.value = receipt; dnsOpen.value = false; void resource.reload() }
watch(selectedId, () => { dnsReceipt.value = undefined; dnsOpen.value = false })
function selectRun(event: Event) { const id = (event.target as HTMLSelectElement).value; if (!id) historyPage.value = 1; void router.replace({query: id ? {run: id} : {}}) }
function toggleSort(field: ResultSort) { direction.value = sort.value === field ? direction.value === 'asc' ? 'desc' : 'asc' : field === 'speed_mb_s' ? 'desc' : 'asc'; sort.value = field; page.value = 1 }
async function stop() { if (run.value && await mutation.submit('/runs/' + segment(run.value.id) + '/cancel')) { stopping.value = false; void resource.reload() } }
</script>
<template>
  <RouterLink to="/tasks" class="back-link">← 优选策略</RouterLink>
  <PageHeader :title="taskResource.data.value?.name || 'IP 结果'" desc="IP 优选结果"><button v-if="run" type="button" class="primary" :disabled="!dnsReady" :title="dnsReady ? '手动选择现有 DNS 记录并推送本次结果' : dnsBlockReason" @click="openDns">推送 DNS</button><RouterLink :to="base + '/edit'" class="button">编辑策略</RouterLink></PageHeader>
  <ResourceState :loading="taskResource.loading.value || history.loading.value || resource.loading.value" :error="taskResource.error.value || history.error.value || resource.error.value" @retry="taskResource.reload(); history.reload(); resource.reload()" />
  <PendingOperationNotice v-if="selectedId" :scope="'/api/v1/runs/' + selectedId" label="本次执行" @resolved="resource.reload" />
  <MutationState v-if="dnsReceipt" :receipt="dnsReceipt" />
  <div v-if="history.data.value" class="results-toolbar"><label>运行时间<select aria-label="运行时间" :value="requestedRun" @change="selectRun"><option value="">最新一次{{ historyPage > 1 ? '（已读取）' : '' }}</option><option v-if="requestedRun && !history.data.value.items.some(item => item.id === requestedRun)" :value="requestedRun">{{ run ? date(run.queued_at) : '当前所选运行' }}</option><option v-for="item in history.data.value.items" :key="item.id" :value="item.id">{{ date(item.queued_at) }} · {{ item.status === 'completed' ? '已完成' : item.status === 'queued' ? '排队中' : item.status === 'running' ? '运行中' : item.status === 'failed' ? '失败' : item.status === 'no_results' ? '无合格结果' : item.status === 'cancelled' ? '已取消' : '已中断' }}</option></select></label><div class="history-pages" v-if="history.data.value.total > 20"><button :disabled="historyPage <= 1" @click="historyPage--">较新记录</button><span class="small">{{ historyPage }} / {{ Math.ceil(history.data.value.total / 20) }}</span><button :disabled="historyPage * 20 >= history.data.value.total" @click="historyPage++">更早记录</button></div><div v-if="run" class="result-state"><StatusBadge :value="run.status" /><span class="small">{{ date(run.queued_at) }}</span><button v-if="active(run)" @click="stopping = true">停止本次</button></div></div>
  <p v-if="runError" role="alert" class="notice danger">{{ runError }}</p>
  <p v-if="run && active(run)" class="notice">{{ run.status === 'queued' ? '等待运行，完成后将在这里显示 IP 结果。' : '正在运行；结果更新后会自动显示，已产生的数据不代表最终排名。' }}</p>
  <p v-if="deliveryWarning" class="notice warning">本次优选结果已保留，DNS 或通知有异常。<RouterLink :to="base + '/runs/' + selectedId">查看推送处理</RouterLink></p>
  <section v-if="run" class="panel m-surface result-panel"><div class="toolbar results-filters"><label>结果范围<select aria-label="结果范围" v-model="eligibility" @change="page = 1"><option value="eligible">合格 IP</option><option value="">全部 IP</option><option value="excluded">未合格 IP</option></select></label><label>地址族<select aria-label="地址族" v-model="family" @change="page = 1"><option value="">全部</option><option value="ipv4">IPv4</option><option value="ipv6">IPv6</option></select></label><span class="small">{{ candidates.data.value ? '共 ' + candidates.data.value.total + ' 个 IP' : '读取中' }} · 点击列头排序</span></div>
    <ResourceState :loading="candidates.loading.value" :error="candidates.error.value" @retry="candidates.reload" />
    <table v-if="candidates.data.value?.items.length" class="responsive-table result-table"><caption class="sr-only">当前运行的 IP 结果，排序作用于所有匹配 IP</caption><thead><tr><th scope="col">IP</th><th v-for="column in resultColumns" :key="column.key" scope="col" :aria-sort="sort === column.key ? direction === 'asc' ? 'ascending' : 'descending' : 'none'"><button class="sort-button" @click="toggleSort(column.key)">{{ column.label }}<span aria-hidden="true">{{ sort === column.key ? direction === 'asc' ? ' ↑' : ' ↓' : ' ↕' }}</span></button></th><th v-if="eligibility !== 'eligible'" scope="col">结果</th></tr></thead><tbody><tr v-for="candidate in candidates.data.value.items" :key="candidate.ip"><td data-label="IP"><code>{{ candidate.ip }}</code></td><td data-label="延迟">{{ metric(candidate.latency_ms, ' ms') }}</td><td data-label="下载速度">{{ metric(candidate.speed_mb_s, ' MB/s') }}</td><td data-label="失败比例">{{ metric(candidate.loss_percent, '%') }}</td><td v-if="eligibility !== 'eligible'" data-label="结果"><StatusBadge :value="candidate.eligible ? 'eligible' : candidate.state" /><span v-if="candidate.reason" class="block small">{{ candidate.reason }}</span></td></tr></tbody></table>
    <p v-else-if="candidates.data.value && !candidates.error.value" class="empty-state">{{ active(run) ? '本次还没有可展示的 IP，请等待运行完成。' : '本次筛选范围没有 IP；可切换“全部 IP”查看。' }}</p>
    <PagerControl v-if="candidates.data.value" :page="page" :total="candidates.data.value.total" :size="50" @change="page = $event" />
  </section>
  <section v-else-if="history.data.value && !selectedId && !history.error.value" class="empty-state"><h2>还没有运行结果</h2><p>在策略列表开启定时，或运行一次。</p><RouterLink to="/tasks">返回策略列表</RouterLink></section>
  <details class="results-advanced"><summary>高级操作与诊断</summary><p class="small muted">仅在需要排查或管理历史时打开，不改变现有自动推送配置。</p><nav class="actions"><RouterLink v-if="run" :to="base + '/runs/' + run.id">投递记录与详细诊断</RouterLink><RouterLink :to="base + '/runs'">管理运行记录</RouterLink><RouterLink :to="base + '/recycle-bin'">回收站</RouterLink><RouterLink :to="base">策略高级管理</RouterLink></nav></details>
  <DnsDeliveryDialog v-if="dnsOpen" :task-id="taskId" :run-id="selectedId" :run="run" @close="dnsOpen = false" @done="dnsDone" />
  <ModalDialog v-if="stopping" title="停止本次运行" @close="stopping = false"><p>只停止本策略的这次运行，不关闭未来定时，也不撤销已经完成的推送。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="stopping = false">返回</button><button class="danger-button" :disabled="mutation.busy.value" @click="stop">确认停止</button></div></ModalDialog>
</template>
