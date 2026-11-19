<script setup lang="ts">
import { computed, nextTick, reactive, ref } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { ApiError, request, segment } from '../api/client'
import { DOWNLOAD4_FIELD, DOWNLOAD6_FIELD, type Connection, type Defaults, type OptimizerConfig, type Schedule, type Source, type Target, type Task } from '../api/types'
import { activeFilterOrder, DEFAULT_CONFIG, editableFilterOrder, label, validateTask } from '../lib/domain'
import { useMutation } from '../lib/useMutation'
import { useResource } from '../lib/useResource'
import { systemTimezone } from '../lib/settings'
import { cloneDto } from '../lib/serialize'
import FieldControl from '../components/FieldControl.vue'
import MutationState from '../components/MutationState.vue'
import ResourceState from '../components/ResourceState.vue'
import TargetSelector from '../components/TargetSelector.vue'
import { automaticActions } from '../lib/resultView'
import EditorTabs from '../components/EditorTabs.vue'

const route = useRoute()
const router = useRouter()
const id = route.params.taskId ? String(route.params.taskId) : ''
const revision = ref<Task['revision']>()
const errors = ref<Record<string, string>>({})
const summary = ref<HTMLElement>()
type EditorSection = 'basic' | 'measure' | 'schedule' | 'advanced'
type AdvancedSection = 'measurement' | 'download' | 'budget' | 'delivery'
const editorSection = ref<EditorSection>('basic')
const advancedSection = ref<AdvancedSection>('measurement')
const sectionTabs = computed(() => [
  { id: 'basic', label: '基本信息', panel: 'basic' },
  { id: 'measure', label: '筛选条件', panel: 'measure' },
  { id: 'schedule', label: '定时计划', panel: 'schedule' },
  { id: 'advanced', label: '高级设置', panel: 'advanced-settings' },
].map(item => ({ ...item, errors: Object.keys(errors.value).filter(key => sectionForField(key) === item.id).length })))
const advancedTabs = computed(() => [
  { id: 'measurement', label: '测速细节', panel: 'advanced-measure' },
  { id: 'download', label: '下载设置', panel: 'download-settings' },
  { id: 'budget', label: '抽样预算', panel: 'budgets' },
  { id: 'delivery', label: 'DNS 与通知', panel: 'delivery' },
].map(item => ({ ...item, errors: Object.keys(errors.value).filter(key => sectionForField(key) === 'advanced' && advancedSectionForField(key) === item.id).length })))
const initial = ref('')
const saved = ref(false)
const form = reactive({
  name: '', config: cloneDto(DEFAULT_CONFIG),
  schedule: { enabled: false, kind: 'interval', interval_minutes: 60, expression: '0 * * * *', time_of_day: '09:00', weekday: 1 } as Schedule,
  dns_auto: false, targets: [] as Target[],
  notifications: { telegram: { enabled: false, connection_id: '' }, wecom: { enabled: false, connection_id: '' } },
})
function populate(task: Task | undefined, defaults: Defaults) {
  form.name = task?.name || ''
  form.config = { ...cloneDto(DEFAULT_CONFIG), ...cloneDto(defaults.config), ...cloneDto(task?.config || {}) }
  form.config.sources = (form.config.sources || []).map(source => ({ ...source, count: source.count === 0 ? undefined : source.count }))
  form.config.filter_order = editableFilterOrder(form.config.filter_order)
  form.schedule = { ...form.schedule, ...task?.schedule, enabled: task?.schedule?.enabled || false }
  form.targets = cloneDto(task?.targets || [])
  form.dns_auto = task?.dns_auto || false
  for (const channel of ['telegram', 'wecom'] as const) form.notifications[channel] = { enabled: task?.notifications?.[channel]?.enabled || false, connection_id: task?.notifications?.[channel]?.connection_id || '' }
  revision.value = task?.revision
  initial.value = JSON.stringify(form)
  errors.value = {}
}
const resource = useResource(async () => {
  const [defaults, connections, task] = await Promise.all([request<Defaults>('/defaults'), request<Connection[]>('/connections'), id ? request<Task>('/tasks/' + segment(id)) : Promise.resolve(undefined)])
  if (!initial.value) populate(task, defaults)
  return { defaults, connections, task }
})
const mutation = useMutation()
const dirty = computed(() => !!initial.value && JSON.stringify(form) !== initial.value && !saved.value)
onBeforeRouteLeave(() => !dirty.value || window.confirm('未保存的配置会丢失，是否离开？已提交的后台操作不会因此取消。'))

type NumericKey = { [Key in keyof OptimizerConfig]: OptimizerConfig[Key] extends number ? Key : never }[keyof OptimizerConfig]
const probeFields: { key: NumericKey; text: string; min: number; max: number }[] = [
  { key: 'probe_count', text: '探测次数', min: 1, max: 100 },
  { key: 'probe_timeout_ms', text: '单次探测等待时间（ms）', min: 100, max: 10000 },
  { key: 'tcp_port', text: 'TCPING 端口', min: 1, max: 65535 },
]
const budgetFields: { key: NumericKey; text: string }[] = [
  { key: 'sample_size', text: '每 CIDR 初始抽样数' }, { key: 'ipv4_limit', text: 'IPv4 总预算' }, { key: 'ipv6_limit', text: 'IPv6 总预算' },
]
const downloadFields: { key: NumericKey; text: string; max: number }[] = [
  { key: DOWNLOAD4_FIELD, text: 'IPv4 最多测速数', max: 100000 }, { key: DOWNLOAD6_FIELD, text: 'IPv6 最多测速数', max: 100000 },
  { key: 'download_window_seconds', text: '正文采样窗口（秒）', max: 60 }, { key: 'prebody_timeout_seconds', text: '正文前合计等待（秒）', max: 60 },
]
const filters: { key: 'latency_enabled' | 'loss_enabled' | 'download_enabled'; threshold: NumericKey; text: string; max?: number; step: string }[] = [
  { key: 'latency_enabled', threshold: 'latency_max_ms', text: '延迟上限（ms，成功探测中位数）', step: '0.01' },
  { key: 'loss_enabled', threshold: 'loss_max_percent', text: '失败比例上限（%）', max: 100, step: '0.01' },
  { key: 'download_enabled', threshold: 'speed_min_mb_s', text: '下载速度下限（MB/s）', step: '0.01' },
]
function moveFilter(index: number, direction: number) {
  const order = form.config.filter_order
  const other = index + direction
  if (other < 0 || other > 1 || index > 1) return
  const value = order[index]!
  order[index] = order[other]!
  order[other] = value
}
function selectSection(section: string) {
  editorSection.value = section as EditorSection
}
function selectAdvancedSection(section: string) {
  editorSection.value = 'advanced'
  advancedSection.value = section as AdvancedSection
}
function sectionForField(key: string): EditorSection {
  if (['name', 'ipv4_enabled', 'ipv6_enabled', 'sources'].includes(key) || key.startsWith('source-count-')) return 'basic'
  if (['probe_mode', 'latency_enabled', 'latency_max_ms', 'loss_enabled', 'loss_max_percent', 'download_enabled', 'speed_min_mb_s'].includes(key)) return 'measure'
  if (['schedule', 'kind', 'interval_minutes', 'expression', 'time_of_day', 'weekday'].includes(key)) return 'schedule'
  return 'advanced'
}
function advancedSectionForField(key: string): AdvancedSection {
  if (key === 'download_url' || downloadFields.some(field => field.key === key)) return 'download'
  if (['sample_size', 'ipv4_limit', 'ipv6_limit'].includes(key)) return 'budget'
  if (['dns_auto', 'targets', 'telegram', 'wecom'].includes(key)) return 'delivery'
  return 'measurement'
}
async function focusField(key: string) {
  const section = sectionForField(key)
  editorSection.value = section
  if (section === 'advanced') {
    advancedSection.value = advancedSectionForField(key)
  }
  await nextTick()
  const element = document.getElementById(key)
  element?.focus({ preventScroll: true })
  element?.scrollIntoView({ block: 'center', behavior: 'instant' })
}
function updateSourceCount(source: Source, event: Event) {
  const input = event.target as HTMLInputElement
  source.count = input.value === '' && !input.validity.badInput ? undefined : input.valueAsNumber
}
function filterEnabled(step: string) {
  return step === 'latency' ? form.config.latency_enabled : step === 'loss' ? form.config.loss_enabled : step === 'download' ? form.config.download_enabled : false
}
async function focusSummary() {
  const firstError = Object.keys(errors.value)[0]
  if (firstError) {
    const section = sectionForField(firstError)
    editorSection.value = section
    if (section === 'advanced') {
      advancedSection.value = advancedSectionForField(firstError)
    }
  }
  await nextTick(); summary.value?.focus()
}
async function reloadLatest() {
  if (!window.confirm('将丢弃本页未保存修改并载入最新版本。请重新复核后保存，是否继续？')) return
  try { const task = await request<Task>('/tasks/' + segment(id)); if (resource.data.value) populate(task, resource.data.value.defaults); mutation.error.value = undefined }
  catch (caught) { mutation.error.value = caught as ApiError }
}
async function submit() {
  errors.value = validateTask({ id, ...form })
  if (Object.keys(errors.value).length) { await focusSummary(); return }
  if (id && revision.value === undefined) { errors.value.revision = '缺少配置版本，不能安全覆盖。请重载最新任务。'; await focusSummary(); return }
  const savedForm = cloneDto({ ...form, config: { ...form.config, filter_order: activeFilterOrder(form.config) } })
  const receipt = await mutation.submit(id ? '/tasks/' + segment(id) : '/tasks', { method: id ? 'PATCH' : 'POST', body: id ? { task: { ...savedForm, id } } : { task: savedForm }, revision: id ? revision.value : undefined })
  if (mutation.error.value?.details.fields) {
    for (const [key, value] of Object.entries(mutation.error.value.details.fields)) errors.value[key.replace('task.', '').replace('config.', '')] = Array.isArray(value) ? value.join('；') : value
    await focusSummary()
  }
  if (receipt?.object_id) { saved.value = true; await router.push('/tasks/' + receipt.object_id + '/results') }
}
</script>
<template>
  <RouterLink to="/tasks" class="back-link">← 优选策略</RouterLink>
  <header class="page-heading editor-heading"><div><h1 tabindex="-1">{{ id ? '编辑策略' : '新建策略' }}</h1><p class="muted">保存只影响后续新执行，不改当次快照、不运行、不发 DNS／通知。保存后可在策略列表直接开启定时。</p></div></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" @retry="resource.reload" />
  <p v-if="resource.data.value?.task?.configuration_issue" class="notice danger" role="alert">{{ resource.data.value.task.configuration_issue }}；敏感地址已移除，定时暂停。请重新填写安全地址并保存；不会自动重跑或恢复计划。</p>
  <form v-if="resource.data.value" class="strategy-editor" novalidate @submit.prevent="submit">
    <div v-if="Object.keys(errors).length" ref="summary" class="notice danger" tabindex="-1" role="alert"><h2>配置未保存，请修正以下内容</h2><ul><li v-for="(message, key) in errors" :key="key"><a :href="'#' + String(key)" @click.prevent="focusField(String(key))">{{ message }}</a></li></ul></div>
    <MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
    <div v-if="id && mutation.error.value?.details.code === 'REVISION_CONFLICT'" class="notice warning"><p>其他共享使用者已修改配置。当前输入已保留，不能自动覆盖。</p><button type="button" @click="reloadLatest">丢弃本页修改，载入最新版本后复核</button></div>
    <EditorTabs id="editor-step" label="策略配置分区" :selected="editorSection" :items="sectionTabs" @select="selectSection" />
    <section tabindex="-1" v-show="editorSection === 'basic'" id="basic" class="section editor-section" role="tabpanel" aria-labelledby="editor-step-basic"><h2 class="editor-section-title">名称与 IP 来源</h2>
      <div class="editor-basics">
      <FieldControl id="name" label="策略名称（必填）" :error="errors.name" v-slot="field"><input :id="field.id" v-model.trim="form.name" required :aria-invalid="field.invalid" :aria-describedby="field.describedby" /></FieldControl>
      <fieldset class="inline-group"><legend>启用地址族（至少一个）</legend><label class="check-line"><input id="ipv4_enabled" v-model="form.config.ipv4_enabled" type="checkbox" />IPv4</label><label class="check-line"><input id="ipv6_enabled" v-model="form.config.ipv6_enabled" type="checkbox" />IPv6</label><p v-if="errors.ipv4_enabled" class="field-error">{{ errors.ipv4_enabled }}</p></fieldset>
      </div>
      <div id="sources" tabindex="-1">
        <h3>组合来源</h3><p class="small muted">“全部 IP 段”指项目内置清单，不是互联网全量扫描；域名在实际执行开始时解析。每行一个来源，可组合使用。</p>
        <div v-for="(source, index) in form.config.sources" :key="index" class="source-row">
          <label>来源 {{ index + 1 }} 类型<select v-model="source.kind" @change="source.count = undefined"><option value="builtin">项目内置清单</option><option value="ip">单个 IP</option><option value="cidr">CIDR</option><option value="domain">域名</option></select></label>
          <label>来源 {{ index + 1 }} 值<select v-if="source.kind === 'builtin'" v-model="source.value"><option value="ipv4">全部 IPv4 段</option><option value="ipv6">全部 IPv6 段</option></select><input v-else v-model.trim="source.value" :aria-label="'来源 ' + (index + 1) + ' 值'" /></label>
          <FieldControl v-if="source.kind === 'cidr'" :id="'source-count-' + index" :label="'来源 ' + (index + 1) + ' 初采数量'" :error="errors['source-count-' + index]" :hint="'留空使用默认初采数量（' + form.config.sample_size + '）'" v-slot="control">
            <input :id="control.id" :value="source.count" type="number" min="1" max="100000" step="1" :aria-invalid="control.invalid" :aria-describedby="control.describedby" @input="updateSourceCount(source, $event)" />
          </FieldControl>
          <button type="button" @click="form.config.sources.splice(index, 1)">删除来源 {{ index + 1 }}</button>
        </div>
        <p v-if="errors.sources" class="field-error">{{ errors.sources }}</p><button type="button" @click="form.config.sources.push({ kind: 'domain', value: '' })">添加来源</button>
      </div>
    </section>
    <section v-show="editorSection === 'measure'" id="measure" class="section editor-section" role="tabpanel" aria-labelledby="editor-step-measure"><h2 class="editor-section-title">筛选条件</h2><label>探测方式<select id="probe_mode" v-model="form.config.probe_mode"><option value="icmp">ICMP Ping</option><option value="tcp">TCPING</option></select></label><p class="small muted">ICMP 不可用时需手动改选 TCPING，不会自动切换。</p><div class="form-grid"><div v-for="filter in filters" :key="filter.key" class="field"><label class="check-line"><input v-model="form.config[filter.key]" type="checkbox" />启用{{ filter.key === 'latency_enabled' ? '延迟过滤' : filter.key === 'loss_enabled' ? '失败比例过滤' : '下载测速与过滤' }}</label><FieldControl :id="filter.threshold" :label="filter.text" :error="errors[filter.threshold]" v-slot="control"><input :id="control.id" v-model.number="form.config[filter.threshold]" type="number" min="0" :max="filter.max" :step="filter.step" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl></div></div></section>
    <section v-show="editorSection === 'schedule'" id="schedule" class="section editor-section" role="tabpanel" aria-labelledby="editor-step-schedule"><h2 class="editor-section-title">定时计划</h2><p>系统时区：{{ systemTimezone || '未知' }}。{{ form.schedule.enabled ? '现有计划已启用；保存会更新未来计划，不影响已有执行。' : '保存后在策略列表开启定时，不会立即试跑。' }}</p><div class="form-grid"><label>计划类型<select id="kind" aria-label="计划类型" v-model="form.schedule.kind"><option value="interval">间隔分钟</option><option value="daily">每天</option><option value="weekly">每周</option><option value="cron">五字段 Cron</option></select></label><FieldControl v-if="form.schedule.kind === 'interval'" id="interval_minutes" label="间隔（分钟，最短 1）" :error="errors.interval_minutes" v-slot="control"><input :id="control.id" v-model.number="form.schedule.interval_minutes" type="number" min="1" step="1" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl><FieldControl v-if="form.schedule.kind === 'cron'" id="expression" label="Cron 表达式" :error="errors.expression" v-slot="control"><input :id="control.id" v-model.trim="form.schedule.expression" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl><FieldControl v-if="['daily', 'weekly'].includes(form.schedule.kind || '')" id="time_of_day" label="执行时刻" :error="errors.time_of_day" v-slot="control"><input :id="control.id" v-model="form.schedule.time_of_day" type="time" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl><label v-if="form.schedule.kind === 'weekly'">星期<select id="weekday" v-model.number="form.schedule.weekday"><option v-for="(day, index) in ['日', '一', '二', '三', '四', '五', '六']" :key="index" :value="index">星期{{ day }}</option></select></label></div></section>
    <section v-show="editorSection === 'advanced'" id="advanced-settings" class="editor-advanced" role="tabpanel" aria-labelledby="editor-step-advanced">
      <EditorTabs id="advanced-tab" label="高级设置分组" :selected="advancedSection" :items="advancedTabs" @select="selectAdvancedSection" />
      <p class="small advanced-safety">已有配置原样保留。当前自动推送：{{ automaticActions({id, ...form}) || '均关闭' }}。</p>
      <section v-show="advancedSection === 'measurement'" id="advanced-measure" class="section editor-section" role="tabpanel" aria-labelledby="advanced-tab-measurement"><h2 class="editor-section-title">测速细节</h2><div class="form-grid"><FieldControl v-for="field in probeFields" :id="field.key" :key="field.key" :label="field.text" :error="errors[field.key]" v-slot="control"><input :id="control.id" v-model.number="form.config[field.key]" type="number" :min="field.min" :max="field.max" step="1" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl></div><div id="filter_order" tabindex="-1"><h3>过滤执行顺序</h3><p class="small muted">停用步骤不执行；保存时仅提交已启用项目，下载过滤固定后置。</p><p v-if="errors.filter_order" class="field-error">{{ errors.filter_order }}</p><ol class="filter-order"><li v-for="(step, index) in form.config.filter_order" :key="step"><span>{{ label(step) }}<span v-if="!filterEnabled(step)" class="small muted">（停用）</span></span><button type="button" :aria-label="'将 ' + label(step) + ' 提前'" :disabled="index !== 1" @click="moveFilter(index, -1)">上移</button><button type="button" :aria-label="'将 ' + label(step) + ' 后移'" :disabled="index !== 0" @click="moveFilter(index, 1)">下移</button><span v-if="step === 'download'" class="small muted">固定后置</span></li></ol></div>
      <FieldControl id="sort" label="结果排序" :error="errors.sort" v-slot="control"><select :id="control.id" v-model="form.config.sort" :aria-invalid="control.invalid" :aria-describedby="control.describedby"><option value="latency">延迟优先</option><option value="speed">下载速度优先</option></select></FieldControl>
    </section>
    <section v-show="advancedSection === 'download'" id="download-settings" class="section editor-section" role="tabpanel" aria-labelledby="advanced-tab-download"><h2 class="editor-section-title">下载设置</h2><FieldControl id="download_url" label="自定义测速文件 URL（HTTP／HTTPS）" :error="errors.download_url" hint="默认来自项目 DefaultDownloadURL；格式有效不代表资源已验证可用。本页不会访问或测试该 URL。" v-slot="control"><input :id="control.id" v-model.trim="form.config.download_url" type="url" :aria-invalid="control.invalid" :aria-describedby="control.describedby" autocomplete="off" /></FieldControl><button type="button" @click="form.config.download_url = resource.data.value!.defaults.config.download_url">将未保存 URL 恢复为项目默认值</button>
      <div class="form-grid"><FieldControl v-for="field in downloadFields" :id="field.key" :key="field.key" :label="field.text" :error="errors[field.key]" v-slot="control"><input :id="control.id" v-model.number="form.config[field.key]" type="number" min="1" :max="field.max" step="1" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl></div><p class="small muted">1 MB = 1,000,000 字节；前置等待与正文窗口独立，不另设正文停滞超时。关闭下载时速度标为未测，不当作 0。</p>
    </section>
    <section v-show="advancedSection === 'budget'" id="budgets" class="section editor-section" role="tabpanel" aria-labelledby="advanced-tab-budget"><h2 class="editor-section-title">初采与扩采预算</h2><div class="form-grid"><FieldControl v-for="field in budgetFields" :id="field.key" :key="field.key" :label="field.text" :error="errors[field.key]" v-slot="control"><input :id="control.id" v-model.number="form.config[field.key]" type="number" min="1" max="100000" step="1" :aria-invalid="control.invalid" :aria-describedby="control.describedby" /></FieldControl></div><p class="small muted">各项 1～100000 整数，0 不代表无限。定向候选超预算会使整次失败；/24 或 /120 扩采仍遵守原来源边界。</p></section>
    <section v-show="advancedSection === 'delivery'" id="delivery" class="section editor-section" role="tabpanel" aria-labelledby="advanced-tab-delivery"><h2 class="editor-section-title">DNS 与通知（独立开关）</h2><label class="check-line"><input id="dns_auto" v-model="form.dns_auto" type="checkbox" />启用 DNS 自动推送</label><div id="targets" tabindex="-1"><p v-if="errors.targets" class="field-error">{{ errors.targets }}</p><TargetSelector v-model="form.targets" /></div><fieldset v-for="channel in (['telegram', 'wecom'] as const)" :key="channel" class="channel-group"><legend>{{ label(channel) }}</legend><label class="check-line"><input v-model="form.notifications[channel].enabled" type="checkbox" />启用该渠道自动通知</label><FieldControl :id="channel" label="引用连接" :error="errors[channel]" v-slot="control"><select :id="control.id" v-model="form.notifications[channel].connection_id" :aria-invalid="control.invalid" :aria-describedby="control.describedby"><option value="">未选择</option><option v-for="connection in resource.data.value.connections.filter(item => item.kind === channel && item.credentials_set)" :key="connection.id" :value="connection.id">{{ connection.name || connection.id }}</option></select></FieldControl></fieldset><p class="small muted">保存不发测试消息。自动通知关闭时，仍可在具体运行详情首次手动发送摘要。</p></section>
</section>
    <footer class="form-footer"><RouterLink class="button" :to="'/tasks'">取消编辑</RouterLink><button class="primary" :disabled="mutation.busy.value || (!!id && revision === undefined)">{{ mutation.busy.value ? '提交中…' : '保存策略' }}</button><span class="small muted">版本 {{ revision ?? '新任务' }} · {{ dirty ? '有未保存修改' : '未修改' }}</span></footer>
  </form>
</template>
