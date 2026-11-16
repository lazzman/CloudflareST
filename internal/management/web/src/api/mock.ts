import { active, DEFAULT_CONFIG, runMatches, validateTask } from '../lib/domain'
import { compareResults, type ResultSort, type ResultDirection } from '../lib/resultView'
import { DOWNLOAD4_FIELD, DOWNLOAD6_FIELD } from './types'
import type { Connection, ConnectionKind, OperationLog, Queue, Run, Settings, Target, Task } from './types'
import { logMatches } from '../lib/logs'

const now = Date.now()
const ago = (hours: number) => new Date(now - hours * 3600000).toISOString()
const lockedTarget: Target = {
  connection_id: 'connection-alidns', account_id: 'demo-account', record_id: 'record-demo-01', target_id: 'target-demo-01',
  domain: 'edge.example.test', rr: 'edge', type: 'A', line: 'default', ttl: 600, value: '198.51.100.8', read_at: ago(0.3),
  last_known_write: { task_id: 'task-weekly', run_id: 'run-demo-002', written_at: ago(0.5), ip: '198.51.100.8' },
  protection: { active: true, revision: 3, reason: 'DNS 响应未确认，自动写入已暂停', created_at: ago(1), attempt_id: 'attempt-demo-unconfirmed' },
}
const connections: Connection[] = [
  { id: 'connection-alidns', kind: 'alidns', name: '生产账号（本地预览）', account_id: 'demo-account', revision: 2, credentials_set: true, referenced_by: ['task-demo'] },
  { id: 'connection-telegram', kind: 'telegram', name: '运行通知', account_id: 'demo-chat', revision: 1, credentials_set: true, referenced_by: ['task-demo'] },
  { id: 'connection-wecom', kind: 'wecom', name: '运维群机器人', account_id: 'demo-room', revision: 1, credentials_set: true, referenced_by: [] },
]
const task: Task = {
  id: 'task-demo', name: '边缘节点 · 每日复核', revision: 4, archived: false,
  config: { ...structuredClone(DEFAULT_CONFIG), ipv6_enabled: true, sources: [{ kind: 'builtin', value: 'ipv4' }, { kind: 'cidr', value: '192.0.2.0/24' }, { kind: 'domain', value: 'cdn.example.test' }] },
  schedule: { enabled: true, kind: 'daily', time_of_day: '09:40', next_run_at: new Date(now + 3600000).toISOString() },
  dns_auto: true, targets: [structuredClone(lockedTarget)],
  notifications: { telegram: { enabled: false, connection_id: 'connection-telegram' }, wecom: { enabled: true, connection_id: 'connection-wecom' } },
}
const queuedTask: Task = { id: 'task-weekly', name: 'IPv6 · 每周抽样', revision: 2, archived: false, config: { ...structuredClone(DEFAULT_CONFIG), ipv4_enabled: false, ipv6_enabled: true }, schedule: { enabled: false, kind: 'interval', interval_minutes: 60 }, dns_auto: false, targets: [], notifications: {} }
const candidates = [
  { ip: '192.0.2.18', family: 'ipv4', eligible: true, rank: 1, state: 'eligible', latency_ms: 41.3, loss_percent: 0, speed_mb_s: 18.2, sources: ['192.0.2.0/24'], probe_attempts: 5, probe_successes: 5, steps: [{ stage: 'latency', state: 'passed', reason: '中位数低于当前阈值' }, { stage: 'loss', state: 'passed', reason: '成功探测比例达到要求' }, { stage: 'download', state: 'passed', reason: '实际下载速度达到当前阈值' }] },
  { ip: '192.0.2.42', family: 'ipv4', eligible: true, rank: 2, state: 'eligible', latency_ms: 58.1, loss_percent: 0, speed_mb_s: 12.4, sources: ['192.0.2.0/24'], probe_attempts: 5, probe_successes: 5, steps: [{ stage: 'latency', state: 'passed' }, { stage: 'loss', state: 'passed' }, { stage: 'download', state: 'passed' }] },
  { ip: '192.0.2.95', family: 'ipv4', eligible: false, state: 'rejected', reason: '下载速度低于门槛', latency_ms: 82.7, loss_percent: null, speed_mb_s: 2.1, steps: [{ stage: 'latency', state: 'passed' }, { stage: 'loss', state: 'not_run', reason: '结果未提供' }, { stage: 'download', state: 'rejected', reason: '低于配置门槛' }] },
  { ip: '2001:db8::18', family: 'ipv6', eligible: true, rank: 1, state: 'eligible', latency_ms: 76.4, loss_percent: 0, speed_mb_s: 9.1, sources: ['2001:db8::/48'], probe_attempts: 5, probe_successes: 5, steps: [{ stage: 'latency', state: 'passed' }, { stage: 'loss', state: 'passed' }, { stage: 'download', state: 'passed' }] },
]
const run: Run = {
  id: 'run-demo-001', task_id: task.id, task_name: task.name, trigger: 'manual', status: 'completed', stage: 'finished', slot_busy: false,
  queued_at: ago(3), started_at: ago(2.9), ended_at: ago(2.8), progress: { stage: 'finished', family: 'ipv6', done: 256, total: 256, unit: '个', message: '本轮本地示例已完成；此数据并非真实测速。' },
  result: { status: 'completed', candidates, sources: [{ id: 'source-demo-1', source: { kind: 'cidr', value: '192.0.2.0/24' }, status: 'completed', candidate_count: 3 }], budgets: [{ family: 'ipv4', limit: 10000, directed: 0, requested_initial: 256, valid_initial: 256, trigger_count: 0, remaining: 9744, initial_limit: 256, sampled: 256, expansion_limit: 9744, expanded: 0, used: 256, initial_reason: '本地样例初采；没有执行真实探测。', expansions: [] }] }, snapshot: structuredClone(task),
  deliveries: [
    { id: 'delivery-demo-dns', attempt_id: 'attempt-demo-unconfirmed', operation_id: 'operation-demo-dns', kind: 'dns', connection_id: 'connection-alidns', target: structuredClone(lockedTarget), ip: '192.0.2.18', state: 'unconfirmed', reason: '预览示例：模拟上游响应未确认；没有发出 DNS 请求。' },
    { id: 'delivery-demo-dns-success', kind: 'dns', connection_id: 'connection-alidns', state: 'success', reason: '混合结果样例；没有执行 DNS 写入。' },
    { id: 'delivery-demo-dns-failed', kind: 'dns', connection_id: 'connection-alidns', state: 'failed', reason: '混合结果样例；没有执行 DNS 写入。' },
    { id: 'delivery-demo-failed', attempt_id: 'attempt-demo-failed', operation_id: 'operation-demo-notify', kind: 'telegram', connection_id: 'connection-telegram', state: 'failed', reason: '本地预览模拟失败，不曾联系 Telegram。', created_at: ago(2.5) },
    { id: 'delivery-demo-sent', attempt_id: 'attempt-demo-sent', kind: 'wecom', connection_id: 'connection-wecom', state: 'success', reason: '本地预览模拟结果；没有发送消息。' },
  ],
}
const waitingRun: Run = { id: 'run-demo-002', task_id: queuedTask.id, task_name: queuedTask.name, trigger: 'scheduled', status: 'queued', stage: 'queued', slot_busy: false, queued_at: ago(0.2), progress: { stage: 'queued', done: 0, total: null, unit: '个', message: '本地预览排队示例，未启动测速。' }, snapshot: structuredClone(queuedTask), result: { status: 'queued', candidates: [], budgets: [], sources: [] }, deliveries: [] }
const tasks: Task[] = [task, queuedTask]
const runs: Run[] = [run, waitingRun]
const logs: OperationLog[] = [
  { id: 'log-preview-1', created_at: ago(0.5), actor: '共享管理身份', action: 'target_protection', object_id: lockedTarget.target_id, state: 'unconfirmed', reason: '预览示例；没有联系 DNS 服务。' },
  { id: 'log-preview-2', created_at: ago(2.9), actor: '共享管理身份', action: 'run_completed', object_id: run.id, state: 'completed', reason: '仅用于本地界面预览。' },
]
const settings: Settings = { timezone: 'Asia/Shanghai', history_days: 30, icmp: { available: true, message: '预览数据；未探测本机 ICMP 能力。' }, password_source: '本地预览认证（内存）' }
const receipts = new Map<string, { operation_id: string; object_id?: string; state: string; result?: unknown }>()
const deleted = new Map<string, Run>()
let authenticated = false

function response(body: unknown, status = 200) { return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } }) }
function fail(status: number, code: string, message: string) { return response({ code, message }, status) }
function getTask(id: string) { return tasks.find(item => item.id === id) }
function getRun(id: string) { return runs.find(item => item.id === id) || deleted.get(id) }
function compactRun(run: Run) {
  const value = structuredClone(run)
  const candidates = value.result?.candidates || []
  value.candidate_summary = {total: candidates.length, families: ['ipv4', 'ipv6'].map(family => {
    const entries = candidates.filter(item => item.family === family)
    const eligible = entries.filter(item => item.eligible)
    return {family, count: entries.length, eligible: eligible.length, best: eligible.find(item => item.rank === 1)}
  })}
  if (value.result) delete value.result.candidates
  value.server_time = new Date().toISOString()
  return value
}
function paginated<Value>(items: Value[], url: URL) {
  const size = Math.max(1, Math.min(1000, Number(url.searchParams.get('page_size')) || 20))
  const page = Math.max(1, Math.min(Math.ceil(items.length / size) || 1, Number(url.searchParams.get('page')) || 1))
  return {items: items.slice((page - 1) * size, page * size), total: items.length, page, page_size: size}
}
function makeReceipt(id: string, objectId?: string) {
  const existing = receipts.get(id)
  if (existing) return existing
  const receipt = { operation_id: id, object_id: objectId, state: 'accepted' }
  receipts.set(id, receipt)
  logs.unshift({ id: 'log-' + id, operation_id: id, created_at: new Date().toISOString(), actor: '共享管理身份（预览）', action: 'mock_action', object_id: objectId, state: 'accepted', reason: '仅修改本地内存；没有调用外部服务。' })
  return receipt
}

export async function mockFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const url = new URL(String(input), 'http://cindy-preview.invalid')
  if (url.origin !== 'http://cindy-preview.invalid' || !url.pathname.startsWith('/api/v1/')) return fail(403, 'PREVIEW_ONLY', '预览适配层只接受本地 /api/v1 路径。')
  const method = init.method || 'GET'
  let body: Record<string, unknown> = {}
  if (init.body && typeof init.body === 'string') { try { body = JSON.parse(init.body) as Record<string, unknown> } catch { return fail(400, 'INVALID_JSON', 'JSON 格式无效。') } }
  if (url.pathname === '/api/v1/session' && method === 'GET') return response({ authenticated, csrf_token: 'local-preview-csrf' })
  if (url.pathname === '/api/v1/session' && method === 'POST') { authenticated = typeof body.password === 'string' && body.password.length > 0; return authenticated ? response({ authenticated: true, csrf_token: 'local-preview-csrf' }) : fail(401, 'AUTH_REQUIRED', '输入任意非空内容模拟通过登录。') }
  if (url.pathname === '/api/v1/session' && method === 'DELETE') { authenticated = false; return response({ state: 'accepted' }) }
  if (!authenticated) return fail(401, 'AUTH_REQUIRED', '请登录本地预览。')
  const path = url.pathname.slice('/api/v1'.length)
  if (path === '/defaults' && method === 'GET') return response({ config: { ...structuredClone(DEFAULT_CONFIG), [DOWNLOAD4_FIELD]: 20, [DOWNLOAD6_FIELD]: 20 }, timezone: settings.timezone, history_days: settings.history_days })
  if (path === '/settings' && method === 'GET') return response(structuredClone(settings))
  if (path === '/settings' && method === 'PATCH') { Object.assign(settings, { timezone: body.timezone, history_days: body.history_days }); const receipt = makeReceipt(String(body.operation_id), 'system'); return response(receipt, 202) }
  if (path === '/tasks' && method === 'GET') return response(structuredClone(tasks.map(task => {
    if (url.searchParams.get('view') !== 'strategies') return task
    const latest = runs.filter(run => run.task_id === task.id && !run.deleted_at).sort((first, second) => (second.queued_at || '').localeCompare(first.queued_at || '') || second.id.localeCompare(first.id))[0]
    return {...task, latest_run: latest ? {id: latest.id, task_id: latest.task_id, status: latest.status, queued_at: latest.queued_at, ended_at: latest.ended_at} : null}
  })))
  if (path === '/tasks' && method === 'POST') {
    const newTask = structuredClone(body.task) as Task
    const fields = validateTask(newTask)
    if (Object.keys(fields).length) return response({ code: 'CONFIG_INVALID', message: '配置不合法。', fields }, 422)
    newTask.id = 'task-local-' + crypto.randomUUID(); newTask.revision = 1
    tasks.unshift(newTask); return response(makeReceipt(String(body.operation_id), newTask.id), 202)
  }
  if (path === '/queue' && method === 'GET') { const activeRun = runs.find(item => item.status === 'running'); return response({ current: activeRun ? compactRun(activeRun) : null, waiting: runs.filter(item => item.status === 'queued').map(compactRun), server_time: new Date().toISOString() } satisfies Queue) }
  if (path === '/connections' && method === 'GET') return response(structuredClone(connections))
  if (path === '/connections' && method === 'POST') {
    const draft = body.connection as Record<string, unknown>
    const connection: Connection = { id: 'connection-local-' + Date.now(), kind: draft.kind as ConnectionKind, name: String(draft.name || ''), account_id: String(draft.account_id || ''), credentials_set: true, revision: 1, referenced_by: [] }
    connections.unshift(connection)
    return response(makeReceipt(String(body.operation_id), connection.id), 202)
  }
  if (path === '/operation-logs' && method === 'GET') return response(logs.filter(item => logMatches(item, url.searchParams)))
  if (path.startsWith('/operations/') && method === 'GET') {
    const id = path.slice('/operations/'.length)
    return response(receipts.get(id) || { operation_id: id, state: 'unknown' })
  }
  if (path.match(/^\/tasks\/[^/]+$/) && method === 'GET') { const item = getTask(path.split('/')[2]!); return item ? response(structuredClone(item)) : fail(404, 'NOT_FOUND', '任务不存在。') }
  if (path.match(/^\/tasks\/[^/]+$/) && method === 'PATCH') {
    const item = getTask(path.split('/')[2]!)
    if (!item) return fail(404, 'NOT_FOUND', '任务不存在。')
    if (String(item.revision) !== String(new Headers(init.headers).get('If-Match'))) return fail(409, 'REVISION_CONFLICT', '版本已变化，请载入最新版本后再复核。')
    const fields = validateTask(body.task as Task)
    if (Object.keys(fields).length) return response({ code: 'CONFIG_INVALID', message: '配置不合法。', fields }, 422)
    const index = tasks.indexOf(item); tasks[index] = { ...item, ...structuredClone(body.task) as object, revision: Number(item.revision || 0) + 1 } as Task
    return response(makeReceipt(String(body.operation_id), item.id), 202)
  }
  if (path.match(/^\/tasks\/[^/]+\/(archive|restore|schedule-state)$/) && method === 'POST') {
    const [, , id, action] = path.split('/')
    const item = getTask(id!)
    if (!item) return fail(404, 'NOT_FOUND', '任务不存在。')
    if (action === 'archive') item.archived = true
    if (action === 'restore') { item.archived = false; item.schedule = { ...item.schedule, enabled: false } }
    if (action === 'schedule-state') item.schedule = { ...item.schedule, enabled: body.enabled === true }
    item.revision = Number(item.revision || 0) + 1
    return response(makeReceipt(String(body.operation_id), item.id), 202)
  }
  if (path.match(/^\/tasks\/[^/]+\/runs$/) && method === 'GET') {
    const id = path.split('/')[2]
    const filtered = runs.filter(item => item.task_id === id && runMatches(item, url.searchParams)).sort((first, second) => (second.queued_at || '').localeCompare(first.queued_at || '') || second.id.localeCompare(first.id)).map(compactRun)
    return response(url.searchParams.has('page') ? paginated(filtered, url) : filtered)
  }
  if (path.match(/^\/tasks\/[^/]+\/recycle-bin$/) && method === 'GET') { const filtered = [...deleted.values()].filter(item => item.task_id === path.split('/')[2] && runMatches(item, url.searchParams)).map(compactRun); return response(url.searchParams.has('page') ? paginated(filtered, url) : filtered) }
  if (path.match(/^\/tasks\/[^/]+\/runs$/) && method === 'POST') {
    const owner = getTask(path.split('/')[2]!)
    if (!owner) return fail(404, 'NOT_FOUND', '任务不存在。')
    const created: Run = { id: 'run-local-' + Date.now(), task_id: owner.id, task_name: owner.name, trigger: 'manual', status: 'queued', stage: 'queued', slot_busy: false, queued_at: new Date().toISOString(), snapshot: structuredClone(owner), progress: { stage: 'queued', done: 0, total: null, message: '仅本地预览；未执行真实测速。' }, result: { status: 'queued', candidates: [], sources: [], budgets: [] }, deliveries: [] }
    runs.unshift(created); return response(makeReceipt(String(body.operation_id), created.id), 202)
  }
  if (path.match(/^\/tasks\/[^/]+\/runs\/[^/]+\/progress$/) && method === 'GET') { const item = getRun(path.split('/')[4]!); if (!item) return fail(404, 'NOT_FOUND', '执行不存在'); const summary = compactRun(item); return response({id: summary.id, status: summary.status, stage: summary.stage, slot_busy: summary.slot_busy, progress: summary.progress, stage_progress: summary.stage_progress, result_status: summary.result?.status, candidate_summary: summary.candidate_summary, deliveries: summary.deliveries, server_time: summary.server_time}) }
  if (path.match(/^\/tasks\/[^/]+\/runs\/[^/]+\/candidates$/) && method === 'GET') {
    const item = getRun(path.split('/')[4]!); if (!item) return fail(404, 'NOT_FOUND', '执行不存在')
    const candidates = (item.result?.candidates || []).filter(candidate => (!url.searchParams.get('family') || candidate.family === url.searchParams.get('family')) && (!url.searchParams.get('eligibility') || candidate.eligible === (url.searchParams.get('eligibility') === 'eligible')))
    const sort = url.searchParams.get('sort'), direction = url.searchParams.get('direction') || 'asc'
    if (sort) {
      if (!['latency_ms', 'speed_mb_s', 'loss_percent'].includes(sort) || !['asc', 'desc'].includes(direction)) return fail(422, 'INVALID_SORT', '排序参数无效')
      candidates.sort((first, second) => compareResults(first, second, sort as ResultSort, direction as ResultDirection))
    }
    return response(paginated(candidates, url))
  }
  if (path.match(/^\/tasks\/[^/]+\/runs\/[^/]+$/) && method === 'GET') { const item = getRun(path.split('/')[4]!); return item ? response(url.searchParams.get('view') === 'summary' ? compactRun(item) : structuredClone(item)) : fail(404, 'NOT_FOUND', '执行记录不存在。') }
  if (path.match(/^\/runs\/[^/]+$/) && method === 'DELETE') {
    const id = path.split('/')[2]!
    const item = getRun(id)
    if (!item || ['running', 'queued'].includes(item.status || '')) return fail(409, 'RUN_NOT_ACTIVE', '示例不允许删除未知或活动执行。')
    deleted.set(id, { ...item, deleted_at: new Date().toISOString(), restore_until: new Date(now + 7 * 86400000).toISOString() })
    runs.splice(runs.indexOf(item), 1)
    return response(makeReceipt(String(new Headers(init.headers).get('Idempotency-Key')), id), 202)
  }
  if (path.match(/^\/runs\/[^/]+\/restore$/) && method === 'POST') { const id = path.split('/')[2]!; const item = deleted.get(id); if (!item) return fail(404, 'NOT_FOUND', '回收记录不存在。'); deleted.delete(id); runs.unshift({ ...item, deleted_at: undefined, restore_until: undefined }); return response(makeReceipt(String(body.operation_id), id), 202) }
  if (path.match(/^\/runs\/[^/]+\/(cancel|dns-deliveries|notifications)$/) && method === 'POST') {
    const id = path.split('/')[2]!
    const action = path.split('/')[3]
    const item = getRun(id)
    if (!item) return fail(404, 'NOT_FOUND', '执行记录不存在。')
    if (action === 'cancel') { item.status = 'cancelled'; item.stage = 'cancelled'; item.slot_busy = false; item.ended_at = new Date().toISOString(); if (item.result) item.result.status = 'cancelled' }
    if (action === 'dns-deliveries') {
      const items = body.items as { target: Target; ip: string }[]
      items.forEach(entry => item.deliveries?.push({ id: 'delivery-local-' + Date.now(), kind: 'dns', connection_id: entry.target.connection_id, target: structuredClone(entry.target), ip: entry.ip, state: 'accepted', reason: '本地模拟受理；没有执行 DNS 写入。' }))
    }
    if (action === 'notifications') {
      const channels = body.channels as { kind: string; connection_id: string; prior_attempt_id?: string }[]
      if (active(item) || item.deleted_at) return fail(409, 'CONFLICT', '运行尚未结束，暂不能发送结束摘要。')
      if (!Array.isArray(channels) || !channels.length || channels.length > 2 || !body.risk_acknowledged) return fail(422, 'INVALID_CHANNELS', '请选择渠道并明确确认范围。')
      const seen = new Set<string>()
      for (const entry of channels) {
        if (!['telegram', 'wecom'].includes(entry.kind) || seen.has(entry.kind) || !connections.some(connection => connection.id === entry.connection_id && connection.kind === entry.kind && connection.credentials_set)) return fail(422, 'INVALID_CHANNELS', '通知渠道或连接无效。')
        seen.add(entry.kind)
        if (entry.prior_attempt_id) {
          const prior = item.deliveries?.find(delivery => delivery.id === entry.prior_attempt_id && delivery.kind === entry.kind && delivery.connection_id === entry.connection_id)
          if (!prior || !['failed', 'unconfirmed'].includes(prior.state || '')) return fail(409, 'CONFLICT', '只能重发失败或未确认的原渠道投递 ID。')
        } else if (item.deliveries?.some(delivery => delivery.kind === entry.kind && ['success', 'pending', 'dispatching', 'running', 'accepted', 'failed', 'unconfirmed', 'unknown'].includes(delivery.state || ''))) return fail(409, 'CONFLICT', '该渠道已有投递，请指定可重试的原尝试。')
      }
      channels.forEach(entry => item.deliveries?.push({ id: 'delivery-local-' + crypto.randomUUID(), operation_id: String(body.operation_id), kind: entry.kind, connection_id: entry.connection_id, state: 'pending', prior_attempt_id: entry.prior_attempt_id, reason: '本地模拟受理，状态保持待确认；没有发送消息。' }))
    }
    return response(makeReceipt(String(body.operation_id), id), 202)
  }
  if (path === '/connections/' + path.split('/')[2] + '/domains' && method === 'GET') return response(['edge.example.test', 'img.example.test'])
  if (path.match(/^\/connections\/[^/]+\/dns-records$/) && method === 'GET') return response(url.searchParams.get('domain') ? [structuredClone(lockedTarget), { ...structuredClone(lockedTarget), record_id: 'record-demo-02', target_id: 'target-demo-02', rr: 'img', type: 'A', value: '192.0.2.18', protection: { active: false, revision: 1 } }].filter(item => item.domain === url.searchParams.get('domain')) : [])
  if (path.match(/^\/connections\/[^/]+$/) && method === 'PATCH') {
    const id = path.split('/')[2]!
    const item = connections.find(value => value.id === id)
    if (!item) return fail(404, 'NOT_FOUND', '连接不存在。')
    const draft = body.connection as Record<string, unknown>
    if (String(item.revision) !== new Headers(init.headers).get('If-Match')) return fail(409, 'REVISION_CONFLICT', '连接版本已变化，请载入最新版本。')
    if (draft.secrets !== undefined && (!draft.secrets || !Object.values(draft.secrets as object).length || Object.values(draft.secrets as object).some(value => typeof value !== 'string' || !value.trim()))) return fail(422, 'INVALID_SECRETS', '替换凭据时须提供完整新值；仅改名时应省略 secrets。')
    Object.assign(item, { name: draft.name || item.name, account_id: draft.account_id || item.account_id, revision: Number(item.revision || 0) + 1 })
    return response(makeReceipt(String(body.operation_id), id), 202)
  }
  if (path.match(/^\/connections\/[^/]+$/) && method === 'DELETE') { const id = path.split('/')[2]!; const index = connections.findIndex(item => item.id === id); if (index < 0) return fail(404, 'NOT_FOUND', '连接不存在。'); connections.splice(index, 1); return response(makeReceipt(String(new Headers(init.headers).get('Idempotency-Key')), id), 202) }
  if (path.match(/^\/dns-targets\/[^/]+\/auto-write-protection\/clear$/) && method === 'POST') { if (Number(body.revision) !== lockedTarget.protection?.revision) return fail(409, 'TARGET_PROTECTION_CHANGED', '保护版本已变化，请重新读取。'); if (lockedTarget.protection) lockedTarget.protection.active = false; return response(makeReceipt(String(body.operation_id), String(path.split('/')[2])), 202) }
  return fail(404, 'NOT_FOUND', method + ' /api/v1' + path + ' 在本地预览适配器中没有对应样例路由。')
}
