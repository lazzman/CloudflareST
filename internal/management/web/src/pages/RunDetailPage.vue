<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { request, segment, query } from '../api/client'
import type { Candidate, Connection, Delivery, Page, ProgressSnapshot, Run, Target } from '../api/types'
import { active, duration, family, label, metric, qualifiedCount, targetKey, targetName } from '../lib/domain'
import { bestCandidates, downloadFacts } from '../lib/runFacts'
import { safeText } from '../lib/safeText'
import { date } from '../lib/settings'
import { firstSummaryChannels, retryConnections, type NotificationKind } from '../lib/notifications'
import { useMutation } from '../lib/useMutation'
import { useResource } from '../lib/useResource'
import ResourceState from '../components/ResourceState.vue'
import MutationState from '../components/MutationState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import TargetFacts from '../components/TargetFacts.vue'
import TargetSelector from '../components/TargetSelector.vue'
import ProtectionControl from '../components/ProtectionControl.vue'
import ModalDialog from '../components/ModalDialog.vue'
import CandidateDecisions from '../components/CandidateDecisions.vue'
import CandidatePicker from '../components/CandidatePicker.vue'
import RunSnapshot from '../components/RunSnapshot.vue'
import StageProgressTable from '../components/StageProgressTable.vue'
import PagerControl from '../components/PagerControl.vue'
const route = useRoute()
const taskId = String(route.params.taskId)
const runId = String(route.params.runId)
const base = '/tasks/' + segment(taskId) + '/runs/' + segment(runId)
const listQuery = computed(() => {
  if (typeof route.query.list !== 'string') return {}
  return Object.fromEntries([...new URLSearchParams(route.query.list)].filter(([key]) => ['start', 'end', 'status', 'trigger', 'dns_state', 'notification_state', 'page'].includes(key)))
})
let previousRun: Run | undefined
let knownConnections: Connection[] = []
const resource = useResource(async () => {
  if (!previousRun) {
    const [run, connections] = await Promise.all([request<Run>(base + '?view=summary'), request<Connection[]>('/connections')])
    previousRun = run
    knownConnections = connections
  } else {
    const progress = await request<ProgressSnapshot>(base + '/progress')
    if (progress.result_status !== previousRun.result?.status || progress.status !== previousRun.status) previousRun = await request<Run>(base + '?view=summary')
    else previousRun = { ...previousRun, ...progress, result: previousRun.result }
  }
  return { run: previousRun, progress: previousRun.progress || {}, connections: knownConnections }
}, { poll: value => active(value?.run) || !!value?.run.deliveries?.some(delivery => ['accepted', 'pending', 'dispatching', 'running'].includes(delivery.state || delivery.status || '')) })
const mutation = useMutation()
const run = computed(() => resource.data.value?.run)
const qualified = computed(() => bestCandidates(run.value))
const channels = computed(() => firstSummaryChannels(run.value?.deliveries || [], resource.data.value?.connections || []))
const ipv4QualifiedCount = computed(() => qualifiedCount(run.value, 'ipv4'))
const ipv6QualifiedCount = computed(() => qualifiedCount(run.value, 'ipv6'))
const hasCandidateResult = computed(() => !!run.value?.candidate_summary || Array.isArray(run.value?.result?.candidates))
const targetSelection = ref<Target[]>([])
const dnsOpen = ref(false)
const cancelOpen = ref(false)
const summaryOpen = ref(false)
const dnsAck = ref(false)
const notificationAck = ref(false)
const notifyForm = reactive({ telegram: '', wecom: '' })
const retryDelivery = ref<Delivery>()
const retryKind = computed(() => retryDelivery.value?.kind || retryDelivery.value?.channel || '')
const retryChoices = computed(() => retryDelivery.value ? retryConnections(retryDelivery.value, run.value?.deliveries || [], resource.data.value?.connections || []) : [])
const statusFilter = ref('')
const candidatePage = ref(1)
const candidateNotice = ref('')
const perPage = 50
const candidateResource = useResource(() => run.value ? request<Page<Candidate>>(base + '/candidates' + query({page: candidatePage.value, page_size: perPage, eligibility: statusFilter.value})) : Promise.resolve({items: [], total: 0, page: 1, page_size: perPage}), {key: () => [candidatePage.value, statusFilter.value, run.value?.candidate_summary?.total, run.value?.result?.status].join(':')})
const candidateWindow = computed(() => candidateResource.data.value?.items || [])
const candidateTotal = computed(() => candidateResource.data.value?.total || 0)
watch(() => candidateResource.data.value?.page, value => { if (value && value !== candidatePage.value) { candidateNotice.value = '候选数量已变化，已返回有效页码。'; candidatePage.value = value } })
const failedDeliveries = computed(() => (run.value?.deliveries || []).filter(item => ['failed', 'unconfirmed', 'unknown'].includes(item.state || item.status || '')))
const selectedIps = ref<Record<string, string>>({})
function metricAt(candidate: Candidate) { return metric(candidate.latency_ms, ' ms') + ' / ' + metric(candidate.loss_percent, '%') + ' / ' + metric(candidate.speed_mb_s, ' MB/s') }
function channelState(kind: string) {
  const items = (run.value?.deliveries || []).filter(item => item.kind === kind || item.channel === kind)
  return items.length ? items.map(item => label(item.state || item.status)).join('、') : '尚无记录／未启用'
}
function canRetry(delivery: Delivery) {
  return retryConnections(delivery, run.value?.deliveries || [], resource.data.value?.connections || []).length > 0
}
function clearNotificationForm() { notifyForm.telegram = ''; notifyForm.wecom = '' }
function openSummary() {
  clearNotificationForm()
  notificationAck.value = false
  mutation.error.value = undefined
  mutation.receipt.value = undefined
  summaryOpen.value = true
}
function closeSummary() { summaryOpen.value = false; notificationAck.value = false; clearNotificationForm() }
function closeRetry() { retryDelivery.value = undefined; notificationAck.value = false; clearNotificationForm() }
function isNewerWrite(target: Target) {
  const previous = Date.parse(target.last_known_write?.written_at || '')
  const source = Date.parse(run.value?.queued_at || '')
  return Number.isFinite(previous) && Number.isFinite(source) && previous > source
}
async function cancelRun() {
  if (!active(run.value)) return
  if (await mutation.submit('/runs/' + segment(runId) + '/cancel')) { cancelOpen.value = false; void resource.reload() }
}
async function sendDns() {
  if (!dnsAck.value || !targetSelection.value.length || targetSelection.value.some(target => !selectedIps.value[targetKey(target)])) return
  const receipt = await mutation.submit('/runs/' + segment(runId) + '/dns-deliveries', { body: { items: targetSelection.value.map(target => ({ target, ip: selectedIps.value[targetKey(target)] })), risk_acknowledged: true } })
  if (receipt) { dnsAck.value = false; targetSelection.value = []; selectedIps.value = {}; dnsOpen.value = false; void resource.reload() }
}
async function sendSummary() {
  const chosen = channels.value.filter(kind => notifyForm[kind])
  if (!chosen.length || !notificationAck.value) return
  const receipt = await mutation.submit('/runs/' + segment(runId) + '/notifications', { body: { channels: chosen.map(kind => ({ kind, connection_id: notifyForm[kind] })), risk_acknowledged: true } })
  if (receipt) { closeSummary(); void resource.reload() }
}
function openRetry(delivery: Delivery) {
  if (!canRetry(delivery)) return
  clearNotificationForm()
  retryDelivery.value = delivery
  notificationAck.value = false
  mutation.error.value = undefined
  mutation.receipt.value = undefined
}
async function retryChannel() {
  const delivery = retryDelivery.value
  const kind = retryKind.value as NotificationKind
  const connectionId = notifyForm[kind]
  if (!delivery || !delivery.id || !retryChoices.value.some(connection => connection.id === connectionId) || !notificationAck.value) return
  const receipt = await mutation.submit('/runs/' + segment(runId) + '/notifications', { body: { channels: [{ kind, connection_id: connectionId, prior_attempt_id: delivery.id }], risk_acknowledged: true } })
  if (receipt) { closeRetry(); void resource.reload() }
}
function downloadText(candidate: Candidate) { return downloadFacts(candidate, run.value?.snapshot?.config) }
</script>
<template>
  <RouterLink :to="{path: '/tasks/' + taskId + '/runs', query: listQuery}" class="back-link">← {{ run?.task_name || taskId }} · 运行记录</RouterLink>
  <header class="page-heading"><div><h1 tabindex="-1">{{ run?.task_name || '执行详情' }}</h1><p class="page-meta">{{ run?.trigger ? label(run.trigger) : '执行详情' }} · <code>{{ runId }}</code></p><p><StatusBadge :value="run?.status" /> 主状态与投递状态独立；当前阶段：{{ label(run?.stage || run?.progress?.stage) }}</p></div><p class="small">开始 {{ date(run?.started_at) }}<br />结束 {{ date(run?.ended_at) }}<br />运行时长 {{ run ? duration(run) : '未知' }}<br />队列时间 {{ date(run?.queued_at) }} · 席位 {{ run?.slot_busy === undefined ? '未知' : run.slot_busy ? '仍占用' : '未占用' }}</p><button v-if="active(run)" class="danger-button" type="button" :disabled="mutation.busy.value" @click="cancelOpen = true">{{ run?.status === 'queued' ? '取消本次排队' : '停止本次执行' }}</button></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <template v-if="run">
    <section v-if="run.reason || run.result?.error" class="notice warning" aria-label="运行结束与优选阶段原因"><h2>运行原因与阶段错误</h2><p v-if="run.reason">运行级原因：{{ safeText(run.reason) }}</p><p v-if="run.result?.error">优选阶段：{{ safeText(run.result.error) }}</p><p class="small">此处解释本次执行，不改写已保存结果；DNS 和通知异常在各自投递明细中查看。</p></section>
    <section class="section"><h2>执行阶段与真实进度</h2><dl class="facts"><div><dt>阶段</dt><dd>{{ label(resource.data.value?.progress.stage || run.stage) }} · {{ resource.data.value?.progress.family || '未知地址族' }}</dd></div><div><dt>已处理／总量</dt><dd>{{ resource.data.value?.progress.done ?? '未知' }} / {{ resource.data.value?.progress.total ?? '待确定' }} {{ resource.data.value?.progress.unit || '' }}</dd></div><div><dt>说明</dt><dd>{{ resource.data.value?.progress.message || '服务端未提供' }}</dd></div><div><dt>最终结论</dt><dd>{{ label(run.result?.status || run.status) }} · IPv4 {{ ipv4QualifiedCount ?? '未知' }} / IPv6 {{ ipv6QualifiedCount ?? '未知' }}</dd></div></dl><p class="small muted">没有服务端总量时显示待确定；不推算总体百分比或剩余时间。失败、跳过、未测均不冒充合格或 0。</p></section>
    <StageProgressTable :stages="run.stage_progress" />
    <section class="section"><h2>当次配置快照</h2><p>任务 {{ run.snapshot?.name || run.task_name || taskId }} · 版本 {{ run.snapshot?.revision ?? '未知' }}</p><dl class="facts"><div><dt>来源</dt><dd><ul><li v-for="(source, index) in run.snapshot?.config?.sources" :key="index">{{ source.kind }} · <code>{{ source.value }}</code></li></ul></dd></div><div><dt>测速文件 URL</dt><dd><code>{{ run.snapshot?.config?.download_url || '未知' }}</code></dd></div><div><dt>过滤顺序</dt><dd>{{ run.snapshot?.config?.filter_order?.map(label).join(' → ') || '未知' }}</dd></div><div><dt>门槛</dt><dd>延迟 ≤{{ run.snapshot?.config?.latency_max_ms ?? '未知' }} ms / 失败比例 ≤{{ run.snapshot?.config?.loss_max_percent ?? '未知' }}% / 下载 ≥{{ run.snapshot?.config?.speed_min_mb_s ?? '未知' }} MB/s</dd></div></dl>
      <RunSnapshot :task="run.snapshot" />
      <h3>候选及逐步骤判定</h3>
      <div class="toolbar"><label>候选筛选<select v-model="statusFilter" @change="candidatePage = 1"><option value="">全部</option><option value="eligible">最终合格</option><option value="excluded">淘汰／其他</option></select></label></div>
      <ResourceState :loading="candidateResource.loading.value" :error="candidateResource.error.value" @retry="candidateResource.reload" />
      <p v-if="candidateNotice" class="notice" role="status">{{ candidateNotice }}</p>
      <table v-if="candidateWindow.length" class="responsive-table"><caption class="sr-only">IP 候选的实际测量和每一步判定</caption><thead><tr><th>IP / 最终资格</th><th>实测值</th><th>步骤及来源</th></tr></thead><tbody><tr v-for="candidate in candidateWindow" :key="candidate.ip"><td data-label="IP / 资格"><code>{{ candidate.ip }}</code> / {{ family(candidate) }}<StatusBadge :value="candidate.eligible ? 'eligible' : candidate.state" /><span v-if="candidate.eligible && candidate.rank" class="block">最终名次 {{ candidate.rank }}</span></td><td data-label="实际测量">{{ metricAt(candidate) }}<span class="block small">{{ downloadText(candidate) }}</span><span class="block small">探测 {{ candidate.probe_successes ?? '未知' }} / {{ candidate.probe_attempts ?? '未知' }}</span></td><td data-label="步骤及来源"><CandidateDecisions :candidate="candidate" :config="run.snapshot?.config" :sources="run.result?.sources" /></td></tr></tbody></table>
      <p v-else-if="!candidateResource.loading.value && !candidateResource.error.value" class="muted">此条件下没有候选，或结果尚未产生；不以预算上限代替实际数量。</p>
      <PagerControl v-if="candidateResource.data.value" :page="candidatePage" :total="candidateTotal" :size="perPage" @change="candidatePage = $event" />
      <h3>来源与预算</h3><details v-for="(item, index) in run.result?.sources || []" :key="index"><summary>来源 {{ index + 1 }}</summary><pre>{{ JSON.stringify(item, null, 2) }}</pre></details><article v-for="(budget, index) in run.result?.budgets || []" :key="budget.family || index" class="target-entry"><h3>{{ budget.family?.toUpperCase() || '未知地址族' }} 预算概况</h3><dl class="facts"><div><dt>累计上限 / 已用 / 剩余</dt><dd>{{ budget.limit ?? '未知' }} / {{ budget.used ?? '未知' }} / {{ budget.remaining ?? '未知' }}</dd></div><div><dt>定向候选</dt><dd>{{ budget.directed ?? '未知' }}</dd></div><div><dt>初采请求 / 有效数量</dt><dd>{{ budget.requested_initial ?? '未知' }} / {{ budget.valid_initial ?? '未知' }}</dd></div><div><dt>初采限额 / 已抽样</dt><dd>{{ budget.initial_limit ?? '未知' }} / {{ budget.sampled ?? '未知' }}</dd></div><div><dt>扩采触发数</dt><dd>{{ budget.trigger_count ?? '未知' }}</dd></div><div><dt>扩采限额 / 已扩采</dt><dd>{{ budget.expansion_limit ?? '未知' }} / {{ budget.expanded ?? '未知' }}</dd></div><div><dt>初采说明</dt><dd>{{ budget.initial_reason || '未提供' }}</dd></div><div><dt>预算说明</dt><dd>{{ budget.reason || '未提供' }}</dd></div></dl><details><summary>完整预算与扩采明细</summary><pre>{{ JSON.stringify(budget, null, 2) }}</pre></details></article>
    </section>
    <section class="section"><header class="page-heading"><div><h2>DNS 与通知投递</h2><p>DNS：{{ channelState('dns') }} · Telegram：{{ channelState('telegram') }} · 企业微信：{{ channelState('wecom') }}</p></div><div class="actions"><button type="button" :disabled="active(run) || !!run.deleted_at || mutation.busy.value" @click="openSummary">发送本次摘要</button><button type="button" :disabled="active(run) || !((ipv4QualifiedCount || 0) + (ipv6QualifiedCount || 0)) || mutation.busy.value" @click="dnsOpen = true">选择现有 DNS 目标</button></div></header>
      <p class="small muted">运行主状态、DNS 和各通知渠道分别展示；查询仅展示事实，不自动写入或解除保护。</p><p v-if="active(run)" class="notice warning">本次执行尚未结束（可能仍在收尾）；结束摘要与手动 DNS 须等待执行结束。</p>
      <div v-if="failedDeliveries.length" class="notice warning"><strong>失败／未确认投递</strong><p>未确认不等于未送达；手动重试可能重复，成功渠道不参与重发。</p></div>
      <article v-for="(delivery, index) in run.deliveries || []" :key="delivery.id || index" class="delivery-row">
        <p><StatusBadge :value="delivery.state || delivery.status" /> {{ label(delivery.kind || delivery.channel) }} · {{ delivery.target ? targetName(delivery.target) : delivery.connection_id || '连接未知' }}<span class="block small">操作 <code>{{ delivery.operation_id || (delivery.automatic ? '系统自动收尾' : '未知') }}</code> / 投递 <code>{{ delivery.id || '未知' }}</code> / 原尝试 <code>{{ delivery.prior_attempt_id || '首次投递／未关联' }}</code></span><span class="block small">{{ delivery.reason || '服务端未提供原因' }}</span></p>
        <dl class="facts"><div><dt>开始 / 结束</dt><dd>{{ date(delivery.started_at) }} → {{ date(delivery.ended_at) }}</dd></div><template v-if="delivery.kind === 'dns'"><div><dt>本次选定写入 IP</dt><dd><code>{{ delivery.ip || '无合格 IP／未选择' }}</code></dd></div><div><dt>写入前读取事实</dt><dd>{{ delivery.before?.value || '未记录／未成功读取' }} · {{ date(delivery.before?.at) }}</dd></div><div><dt>超时后只读核对事实</dt><dd>{{ delivery.readback?.value || '未执行或未取得有效核对值' }} · {{ date(delivery.readback?.at) }}</dd></div><div><dt>核对与实际请求</dt><dd>{{ delivery.verified ? '已通过读取核验' : '未标记读取确认，以投递状态为准' }}；{{ delivery.no_write ? '目标值相同，未发送写请求' : delivery.state === 'skipped' ? '已跳过，未发送写请求' : '不根据状态反推远端是否改变' }}</dd></div></template></dl>
        <details v-if="delivery.target"><summary>明确目标与独立保护状态</summary><TargetFacts :target="delivery.target" /><ProtectionControl :target="delivery.target" @changed="resource.reload" /></details>
        <button v-if="canRetry(delivery)" type="button" @click="openRetry(delivery)">单独重发此渠道</button>
      </article>
      <p v-if="!run.deliveries?.length" class="muted">尚无投递记录；仍可首次手动发送摘要。</p>
    </section>
    <ModalDialog v-if="cancelOpen" :title="run?.status === 'queued' ? '取消本次排队' : '停止本次执行'" @close="cancelOpen = false"><p>任务 {{ run?.task_name || taskId }} · 执行 <code>{{ runId }}</code> · 当前阶段 {{ label(run?.stage) }}</p><p class="notice warning">只作用于本次执行；已完成的 DNS 写入和已送达消息不会撤回。受理停止请求不等于执行已经结束，请继续核查真实状态。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="cancelOpen = false">返回</button><button class="danger-button" :disabled="mutation.busy.value" @click="cancelRun">确认停止或取消本次执行</button></div></ModalDialog>
</template>
  <ModalDialog v-if="dnsOpen" title="从本次结果推送至现有 DNS 记录" @close="dnsOpen = false"><p>来源执行：<code>{{ runId }}</code> · 时间 {{ date(run?.started_at || run?.queued_at) }}。仅更换记录 IP，不创建记录、不修改 TTL、线路或其他目标。</p><TargetSelector v-model="targetSelection" /><article v-for="target in targetSelection" :key="targetKey(target)" class="target-entry"><TargetFacts :target="target" /><CandidatePicker v-model="selectedIps[targetKey(target)]" :task-id="taskId" :run-id="runId" :record-type="target.type" /><p v-if="isNewerWrite(target)" class="notice warning">较旧的来源运行可能覆盖之后的已确认写入。请复核目标、last_known_write 和实际当前值。</p><p v-if="target.protection?.active" class="notice warning">此目标自动写入已暂停，待人工处理。手动投递不解除暂停。</p></article><label class="check-line"><input v-model="dnsAck" type="checkbox" />我已逐条核对账号、记录身份、读取值、last_known_write、IP 族及覆盖风险，同意只更新所选现有目标</label><p class="notice warning">提交前生成固定操作号；断线时只读核查原号，不换号盲重试。解除保护是独立操作。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="dnsOpen = false">取消</button><button class="primary" :disabled="!dnsAck || !targetSelection.length || targetSelection.some(target => !selectedIps[targetKey(target)]) || mutation.busy.value" @click="sendDns">确认提交已复核目标</button></div></ModalDialog>
  <ModalDialog v-if="summaryOpen" title="首次发送本次摘要" @close="closeSummary"><p>任务 {{ run?.task_name || taskId }} · 执行 <code>{{ runId }}</code> · {{ date(run?.queued_at) }}</p><p>结论 {{ label(run?.status || run?.result?.status) }} · 最终合格 IPv4 {{ ipv4QualifiedCount ?? '未知' }} / IPv6 {{ ipv6QualifiedCount ?? '未知' }}</p><p>最佳实测：{{ qualified[0]?.ip || (hasCandidateResult ? '无合格 IP' : '结果未知') }} · {{ qualified[0] ? metricAt(qualified[0]) : '未测' }}</p><p class="notice">仅发送本次所选渠道摘要；不重跑测速、不改 DNS、不改变自动通知开关。自动通知关闭时仍可手动发送。</p><fieldset><legend>选择可用渠道连接</legend><label v-for="kind in channels" :key="kind">{{ label(kind) }}<select v-model="notifyForm[kind]" :aria-label="label(kind)"><option value="">不发送此渠道</option><option v-for="connection in resource.data.value?.connections?.filter(item => item.kind === kind && item.credentials_set)" :key="connection.id" :value="connection.id">{{ connection.name || connection.id }}</option></select></label></fieldset><p class="small muted">只列出未曾投递且有有效连接的渠道；已有失败／未确认请在原投递处单独重发，成功或待确认渠道不重复发送。</p><p v-if="!channels.length" class="notice warning">没有可首次发送的渠道。请核对已有投递；缺少有效连接时可前往 <RouterLink to="/connections">服务连接</RouterLink> 配置，不会自动改绑历史引用。</p><label class="check-line"><input v-model="notificationAck" type="checkbox" />我已核对摘要、任务和所选渠道，同意发送所选渠道</label><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="closeSummary">取消</button><button class="primary" :disabled="!channels.some(kind => notifyForm[kind]) || !notificationAck || mutation.busy.value" @click="sendSummary">发送所选渠道摘要</button></div></ModalDialog>
  <ModalDialog v-if="retryDelivery" title="重发失败／未确认渠道" @close="closeRetry"><p>原操作 <code>{{ retryDelivery.operation_id || '未知' }}</code> · 投递 <code>{{ retryDelivery.id || '未知' }}</code> · 原尝试 <code>{{ retryDelivery.attempt_id || '未知' }}</code></p><p>渠道 {{ label(retryDelivery.kind || retryDelivery.channel) }} · 状态 {{ label(retryDelivery.state) }}</p><p class="notice danger">未确认不等于未送达，重发可能造成重复通知。仅选择本渠道，成功渠道不会重发。</p><fieldset><legend>明确重选可用连接</legend><label>{{ label(retryKind) }}<select v-model="notifyForm[retryKind as NotificationKind]" :aria-label="label(retryKind)"><option value="">明确选择原有效连接</option><option v-for="connection in retryChoices" :key="connection.id" :value="connection.id">{{ connection.name || connection.id }}</option></select></label><p v-if="!retryChoices.length" class="notice warning">原连接不可用或已有后续尝试，不能自动改绑或重复重发。请核对投递并前往 <RouterLink to="/connections">服务连接</RouterLink> 处理。</p></fieldset><label class="check-line"><input v-model="notificationAck" type="checkbox" />我理解可能重复，明确重发所选渠道并关联原尝试</label><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="closeRetry">不重发</button><button class="danger-button" :disabled="!notificationAck || !retryChoices.some(connection => connection.id === notifyForm[retryKind as NotificationKind]) || mutation.busy.value" @click="retryChannel">确认重发本渠道</button></div></ModalDialog>
</template>
