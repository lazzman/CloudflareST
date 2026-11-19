import { DOWNLOAD4_FIELD, DOWNLOAD6_FIELD, type Candidate, type OptimizerConfig, type Run, type Target, type Task } from '../api/types'
import { sensitiveDownloadURL } from './safeText'

export const RUN_STATUSES = ['queued', 'running', 'completed', 'no_results', 'failed', 'cancelled', 'interrupted'] as const
export const ADDRESS_FAMILIES = ['ipv4', 'ipv6'] as const
export type AddressFamily = typeof ADDRESS_FAMILIES[number]

export const DEFAULT_CONFIG: OptimizerConfig = {
  ipv4_enabled: true, ipv6_enabled: false, sources: [{ kind: 'builtin', value: 'ipv4' }],
  sample_size: 256, ipv4_limit: 10000, ipv6_limit: 10000, probe_mode: 'icmp', tcp_port: 443,
  probe_count: 5, probe_timeout_ms: 1000, latency_enabled: true, loss_enabled: true,
  download_enabled: true, latency_max_ms: 200, loss_max_percent: 20, speed_min_mb_s: 5,
  filter_order: ['latency', 'loss', 'download'], sort: 'latency', [DOWNLOAD4_FIELD]: 20,
  [DOWNLOAD6_FIELD]: 20, download_window_seconds: 10, prebody_timeout_seconds: 10,
  download_url: 'https://download.parallels.com/desktop/v15/15.1.5-47309/ParallelsDesktop-15.1.5-47309.dmg',
}
const labels: Record<string, string> = {
  queued: '排队中', waiting: '等待中', running: '运行中', completed: '已完成', success: '成功', succeeded: '成功',
  failed: '失败', canceled: '已取消', cancelled: '已取消', interrupted: '已中断', no_result: '无合格结果', no_eligible: '无合格结果',
  unknown: '结果未知', unconfirmed: '结果未确认', expired: '收据已过期', pending: '等待确认', accepted: '已受理',
  skipped: '已跳过', disabled: '未启用', passed: '通过', rejected: '淘汰', eligible: '最终合格', not_run: '未执行',
  manual: '手动', scheduled: '定时', schedule: '定时', system: '系统', shared: '共享管理身份',
  sources: '解析来源', sampling: '初始抽样', expansion: '扩展采样', probe: '探测', latency: '延迟过滤',
  loss: '失败比例过滤', download: '下载测速', sorting: '结果排序', dns: 'DNS 投递', notifications: '通知收尾',
  notification: '通知', telegram: 'Telegram', wecom: '企业微信', alidns: '阿里云 DNS', committed: '已确认', finished: '已结束', inactive: '未启用',
  cancellation_notifications: '取消通知收尾', finalizing: '收尾处理中', no_results: '无合格结果', filtered: '已淘汰',
  filtering: '过滤判定',
  dispatching: '投递中', untested: '未测试', cleared: '已解除', restored: '已恢复', reviewed: '已核查', recycled: '已移入回收站', removed: '已永久清理',
}
const filterStages = ['latency', 'loss', 'download'] as const
type FilterStage = typeof filterStages[number]
const filterEnabledKeys: Record<FilterStage, keyof OptimizerConfig> = {
  latency: 'latency_enabled', loss: 'loss_enabled', download: 'download_enabled',
}
export const label = (value?: string) => value ? labels[value] || value : '未知'
export function tone(value?: string) {
  if (['failed', 'rejected', 'interrupted', 'filtered'].includes(value || '')) return 'danger'
  if (['completed', 'success', 'succeeded', 'eligible', 'passed', 'committed'].includes(value || '')) return 'success'
  if (['unconfirmed', 'unknown', 'expired', 'pending', 'running', 'queued', 'accepted', 'dispatching', 'finalizing', 'cancellation_notifications'].includes(value || '')) return 'warning'
  return 'neutral'
}
export const active = (run?: Run | null) => !!run && (run.slot_busy === true || ['running', 'queued', 'waiting'].includes(run.status || ''))
export const metric = (value: number | null | undefined, unit = '') => typeof value === 'number' && Number.isFinite(value) ? String(Number(value.toFixed(2))) + unit : '未测／未知'
export function date(value: string | undefined, timezone: string | undefined) {
  if (!value || Number.isNaN(Date.parse(value))) return '未知'
  if (!timezone) return value + '（系统时区未知）'
  try { return new Intl.DateTimeFormat('zh-CN', { timeZone: timezone, dateStyle: 'short', timeStyle: 'medium', hour12: false }).format(new Date(value)) } catch { return value + '（系统时区无效）' }
}
export function duration(run: Run) {
  if (!run.started_at) return ['queued', 'waiting'].includes(run.status || '') ? '尚未取得席位' : '未实际开始／开始时间未知'
  const end = run.ended_at ? Date.parse(run.ended_at) : active(run) ? Date.now() : NaN
  const seconds = Math.floor((end - Date.parse(run.started_at)) / 1000)
  return Number.isFinite(seconds) && seconds >= 0 ? Math.floor(seconds / 60) + ' 分 ' + seconds % 60 + ' 秒（含收尾）' : '未知'
}
export const targetName = (target: Target) => (target.rr || '@') + '.' + (target.domain || '未知域名') + ' · ' + (target.type || '未知类型') + ' · ' + target.record_id
export const targetKey = (target: Target) => target.target_id || (target.account_id || target.connection_id) + ':' + target.record_id
export function family(candidate: Candidate) { return (candidate.family || (candidate.ip.includes(':') ? 'ipv6' : 'ipv4')).toLowerCase() }
export function matchingCandidates(candidates: Candidate[], target: Target) {
  const expected = target.type === 'A' ? 'ipv4' : target.type === 'AAAA' ? 'ipv6' : ''
  return expected ? candidates.filter(candidate => candidate.eligible === true && family(candidate) === expected) : []
}
export function eligible(run?: Run) { return (run?.result?.candidates || []).filter(candidate => candidate.eligible === true) }
export function qualifiedCount(run: Run | undefined, addressFamily: string) {
  if (run && active(run) && !['completed', 'no_results'].includes(run.result?.status || '')) return undefined
  const summary = run?.candidate_summary?.families.find(item => item.family === addressFamily)
  if (summary) return summary.eligible
  const candidates = run?.result?.candidates
  if (!Array.isArray(candidates)) return undefined
  return candidates.filter(candidate => candidate.eligible === true && family(candidate) === addressFamily).length
}
export interface FamilyResult {
  family: AddressFamily
  state: 'disabled' | 'unproduced' | 'unknown' | 'empty' | 'ready'
  count?: number
  best?: Candidate
}
export function familyResult(run: Run, addressFamily: AddressFamily): FamilyResult {
  if (run.snapshot?.config?.[addressFamily === 'ipv4' ? 'ipv4_enabled' : 'ipv6_enabled'] === false) return { family: addressFamily, state: 'disabled' }
  const summary = run.candidate_summary?.families.find(item => item.family === addressFamily)
  if (summary && (summary.count > 0 || ['completed', 'no_results'].includes(run.result?.status || run.status || ''))) return { family: addressFamily, state: summary.eligible ? 'ready' : 'empty', count: summary.eligible, best: summary.best }
  const candidates = run.result?.candidates
  const hasOutcome = ['completed', 'no_results'].includes(run.result?.status || '') || ['completed', 'no_results'].includes(run.status || '')
  if (!Array.isArray(candidates)) return { family: addressFamily, state: active(run) ? 'unproduced' : 'unknown' }
  const measured = candidates.filter(candidate => family(candidate) === addressFamily)
  if (!measured.length && !hasOutcome) return { family: addressFamily, state: 'unproduced' }
  const qualified = measured.filter(candidate => candidate.eligible === true)
  if (!qualified.length && measured.some(candidate => candidate.eligible === undefined)) return { family: addressFamily, state: 'unknown' }
  return { family: addressFamily, state: qualified.length ? 'ready' : 'empty', count: qualified.length, best: qualified.find(candidate => candidate.rank === 1) }
}
export function familyResultText(result: FamilyResult) {
  return result.state === 'disabled' ? '未启用' : result.state === 'unproduced' ? '未产生' : result.state === 'unknown' ? '未知' : result.state === 'empty' ? '合格 0（无合格结果）' : '合格 ' + result.count
}
export function deliverySummary(run: Run, kind: 'dns' | 'telegram' | 'wecom') {
  const deliveries = (run.deliveries || []).filter(delivery => (delivery.kind || delivery.channel || (delivery.target ? 'dns' : '')) === kind)
  if (deliveries.length) return [...new Set(deliveries.map(delivery => label(delivery.state || delivery.status)))].join('、')
  const enabled = kind === 'dns' ? run.snapshot?.dns_auto : run.snapshot?.notifications?.[kind]?.enabled
  return enabled === false ? '未启用／尚无投递' : active(run) ? '尚未投递' : '未知／尚无投递'
}
export function restoreDeadline(run: Run) {
  const deletedAt = Date.parse(run.deleted_at || '')
  return Number.isFinite(deletedAt) ? new Date(deletedAt + 7 * 24 * 60 * 60 * 1000).toISOString() : undefined
}
export function runMatches(run: Run, filters: URLSearchParams) {
  const queuedAt = Date.parse(run.queued_at || '')
  const start = filters.get('start')
  const end = filters.get('end')
  if (start && !(queuedAt >= Date.parse(start)) || end && !(queuedAt <= Date.parse(end))) return false
  if (filters.get('status') && filters.get('status') !== run.status || filters.get('trigger') && filters.get('trigger') !== run.trigger) return false
  return ([['dns_state', true], ['notification_state', false]] as const).every(([key, dns]) => {
    const state = filters.get(key)
    return !state || !!run.deliveries?.some(delivery => ((delivery.kind || delivery.channel || (delivery.target ? 'dns' : '')) === 'dns') === dns && (delivery.state || delivery.status) === state)
  })
}
export function activeFilterOrder(config: Partial<OptimizerConfig>) {
  return (config.filter_order || []).filter((stage): stage is FilterStage =>
    filterStages.includes(stage as FilterStage) && config[filterEnabledKeys[stage as FilterStage]] === true,
  )
}
export function editableFilterOrder(order: readonly string[] | undefined) {
  const existing = (order || []).filter((stage, index, list): stage is FilterStage =>
    filterStages.includes(stage as FilterStage) && list.indexOf(stage) === index,
  )
  return [...existing.filter(stage => stage !== 'download'), ...filterStages.filter(stage => stage !== 'download' && !existing.includes(stage)), 'download']
}
export function scheduleText(task: Task) {
  const plan = task.schedule
  if (!plan) return '未知计划'
  if (plan.kind === 'interval') return '每 ' + (plan.interval_minutes ?? '未知') + ' 分钟'
  if (plan.kind === 'daily') return '每天 ' + (plan.time_of_day || '未知')
  if (plan.kind === 'weekly') return '周' + (['日', '一', '二', '三', '四', '五', '六'][plan.weekday ?? -1] || '未知') + ' ' + (plan.time_of_day || '未知')
  return plan.expression || '未配置计划'
}
export function validateTask(task: Task): Record<string, string> {
  const errors: Record<string, string> = {}
  const config = task.config || {}
  if (!task.name?.trim()) errors.name = '请输入任务名称'
  if (!config.ipv4_enabled && !config.ipv6_enabled) errors.ipv4_enabled = '至少启用一个地址族'
  if (!config.sources?.length) errors.sources = '至少配置一个来源'
  if (config.sources?.some(source => !source.value.trim())) errors.sources = '来源值不能为空'
  config.sources?.forEach((source, index) => {
    if (source.kind === 'cidr' && source.count !== undefined && (!Number.isInteger(source.count) || source.count < 1 || source.count > 100000)) errors['source-count-' + index] = '来源 ' + (index + 1) + ' 初采数量须为 1～100000 的整数，或留空使用默认值'
  })
  const integerRanges: [keyof OptimizerConfig, number, number][] = [
    ['sample_size', 1, 100000], ['ipv4_limit', 1, 100000], ['ipv6_limit', 1, 100000],
    ['probe_count', 1, 100], ['probe_timeout_ms', 100, 10000], ['tcp_port', 1, 65535],
    [DOWNLOAD4_FIELD, 1, 100000], [DOWNLOAD6_FIELD, 1, 100000],
    ['download_window_seconds', 1, 60], ['prebody_timeout_seconds', 1, 60],
  ]
  integerRanges.forEach(([key, min, max]) => {
    const value = config[key]
    if (typeof value !== 'number' || !Number.isInteger(value) || value < min || value > max) errors[key] = '请输入 ' + min + '～' + max + ' 的整数'
  })
  for (const [key, max] of [['latency_max_ms', Infinity], ['loss_max_percent', 100], ['speed_min_mb_s', Infinity]] as const) {
    const value = config[key]
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0 || (key === 'latency_max_ms' && value === 0) || value > max) {
      errors[key] = key === 'latency_max_ms'
        ? '请输入大于 0 的有限数值'
        : '请输入不小于 0' + (Number.isFinite(max) ? '、不大于 ' + max : '') + ' 的数值'
    }
  }
  const order = config.filter_order
  const knownStages = new Set<string>()
  const invalidOrder = !Array.isArray(order) || order.some(stage => {
    if (!filterStages.includes(stage as FilterStage) || knownStages.has(stage)) return true
    knownStages.add(stage)
    return false
  }) || (!!order && order.includes('download') && order.indexOf('download') !== order.length - 1)
  const enabledStages = filterStages.filter(stage => config[filterEnabledKeys[stage]] === true)
  if (invalidOrder || enabledStages.some(stage => !order?.includes(stage))) errors.filter_order = '过滤顺序必须使用唯一的有效项目，并包含全部已启用过滤；下载过滤须排在最后'
  if (config.download_enabled && config.ipv4_enabled && typeof config[DOWNLOAD4_FIELD] === 'number' && config.ipv4_limit && config[DOWNLOAD4_FIELD] > config.ipv4_limit) {
    errors[DOWNLOAD4_FIELD] = 'IPv4 最多测速数不能超过 IPv4 总预算'
  }
  if (config.download_enabled && config.ipv6_enabled && typeof config[DOWNLOAD6_FIELD] === 'number' && config.ipv6_limit && config[DOWNLOAD6_FIELD] > config.ipv6_limit) {
    errors[DOWNLOAD6_FIELD] = 'IPv6 最多测速数不能超过 IPv6 总预算'
  }
  try {
    const url = new URL(config.download_url || '')
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error()
  } catch { errors.download_url = '请输入不含凭据的有效 HTTP／HTTPS 测速文件地址' }
  if (sensitiveDownloadURL(config.download_url || '')) errors.download_url = '测速文件 URL 不允许含认证 Token、密钥或签名参数，请改用不含秘密的文件地址'
  if (!config.download_enabled && config.sort === 'speed') errors.sort = '下载关闭时不可选择速度优先，请改用延迟排序'
  if (task.schedule?.kind === 'interval' && (!Number.isInteger(task.schedule.interval_minutes) || (task.schedule.interval_minutes || 0) < 1)) errors.interval_minutes = '间隔必须是至少 1 分钟的整数'
  if (task.schedule?.kind === 'cron' && task.schedule.expression?.trim().split(' ').filter(Boolean).length !== 5) errors.expression = '请输入五字段 Cron，最短周期为 1 分钟；服务端将进一步核验'
  if (['daily', 'weekly'].includes(task.schedule?.kind || '') && !/^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(task.schedule?.time_of_day || '')) errors.time_of_day = '请选择有效的时刻'
  if (task.dns_auto && !task.targets?.length) errors.targets = 'DNS 自动推送需要至少一个现有目标'
  for (const channel of ['telegram', 'wecom'] as const) {
    if (task.notifications?.[channel]?.enabled && !task.notifications[channel]?.connection_id) errors[channel] = '启用自动通知时必须选择对应连接'
  }
  return errors
}
