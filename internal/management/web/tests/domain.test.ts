import { describe, expect, it } from 'vitest'
import { reactive } from 'vue'
import { DEFAULT_CONFIG, RUN_STATUSES, active, activeFilterOrder, deliverySummary, duration, editableFilterOrder, familyResult, familyResultText, label, matchingCandidates, metric, qualifiedCount, restoreDeadline, runMatches, targetKey, validateTask } from '../src/lib/domain'
import { logMatches, logQuery } from '../src/lib/logs'
import { DOWNLOAD4_FIELD } from '../src/api/types'
import { cloneDto, timeQuery } from '../src/lib/serialize'
import type { Candidate, Run, Target, Task } from '../src/api/types'

function task(): Task {
  return { id: 'task-test', name: '本地测试任务', config: cloneDto(DEFAULT_CONFIG), schedule: { enabled: false, kind: 'interval', interval_minutes: 60 }, notifications: {}, targets: [] }
}
describe('任务配置与测量语义', () => {
  it('项目默认配置合法且四类自动动作默认关闭', () => {
    const draft = task()
    expect(validateTask(draft)).toEqual({})
    expect(draft.config?.ipv4_enabled).toBe(true)
    expect(draft.config?.ipv6_enabled).toBe(false)
    expect(draft.schedule?.enabled).toBe(false)
    expect(draft.dns_auto).toBeUndefined()
  })
  it('使用精确的 Go JSON 配置字段而不是 UI 私有别名', () => {
    const serialized = JSON.parse(JSON.stringify(DEFAULT_CONFIG))
    for (const suffix of ['ipv4', 'ipv6']) expect(serialized[['download', suffix, 'count'].join('_')]).toBe(20)
  })
  it.each([undefined, 1, 100000])('CIDR 接受默认或有效数量 %s，各来源独立', count => {
    const draft = task()
    draft.config!.sources = [{ kind: 'cidr', value: '192.0.2.0/24', count }, { kind: 'cidr', value: '198.51.100.0/24', count: 32 }]
    expect(validateTask(draft)).toEqual({})
    const sources = cloneDto(draft).config?.sources
    if (!sources) throw new Error('测试任务应保留来源列表')
    expect(sources[1]?.count).toBe(32)
    if (count === undefined) expect(sources[0]).not.toHaveProperty('count')
    else expect(sources[0]?.count).toBe(count)
  })
  it.each([0, -1, 100001, 1.5, NaN, Infinity])('CIDR 拒绝数量 %s 并定位具体来源控件', count => {
    const draft = task()
    draft.config!.sources = [{ kind: 'cidr', value: '192.0.2.0/24' }, { kind: 'cidr', value: '198.51.100.0/24', count }]
    expect(validateTask(draft)).toEqual({ 'source-count-1': expect.stringContaining('来源 2') })
  })
  it('下载关闭与速度排序冲突定位排序控件', () => {
    const draft = task()
    draft.config!.download_enabled = false
    draft.config!.sort = 'speed'
    expect(validateTask(draft).sort).toContain('不可选择速度优先')
  })
  it.each([1, 100])('接受探测次数边界 %s', value => {
    const draft = task()
    draft.config!.probe_count = value
    expect(validateTask(draft).probe_count).toBeUndefined()
  })
  it.each([0, 101, 1.5])('拒绝越界或非整数探测次数 %s', value => {
    const draft = task()
    draft.config!.probe_count = value
    expect(validateTask(draft).probe_count).toBeTruthy()
  })
  it.each([100, 10000])('接受单次等待边界 %s ms', value => {
    const draft = task()
    draft.config!.probe_timeout_ms = value
    expect(validateTask(draft).probe_timeout_ms).toBeUndefined()
  })
  it.each([99, 10001, 100.5])('拒绝单次等待 %s ms', value => {
    const draft = task()
    draft.config!.probe_timeout_ms = value
    expect(validateTask(draft).probe_timeout_ms).toBeTruthy()
  })
  it.each([Number.MIN_VALUE, 60000, 60001, 1_000_000_000, Number.MAX_VALUE])('延迟门槛接受有限正数 %s ms，不另设上限', value => {
    const draft = task()
    draft.config!.latency_max_ms = value
    expect(validateTask(draft)).toEqual({})
  })
  it.each([0, -1, NaN, Infinity, -Infinity, '60001', '非数字', '', null, undefined])('延迟门槛拒绝非有限正数 %s', value => {
    const draft = task()
    Object.assign(draft.config!, { latency_max_ms: value })
    expect(validateTask(draft)).toEqual({ latency_max_ms: expect.any(String) })
  })
  it.each([
    ['loss_max_percent', 0], ['loss_max_percent', 100],
    ['speed_min_mb_s', 0], ['speed_min_mb_s', Number.MAX_VALUE],
  ] as const)('其余门槛 %s 保留合法边界 %s', (key, value) => {
    const draft = task()
    draft.config![key] = value
    expect(validateTask(draft)).toEqual({})
  })
  it.each(['loss_max_percent', 'speed_min_mb_s'] as const)('其余门槛 %s 仍拒绝负数、非有限数与非数字', key => {
    for (const value of [-1, NaN, Infinity, -Infinity, '0', '非数字', '', null, undefined]) {
      const draft = task()
      Object.assign(draft.config!, { [key]: value })
      expect(validateTask(draft)).toEqual({ [key]: expect.any(String) })
    }
  })
  it.each([100.01, 60001])('失败比例仍拒绝超过 100%% 的门槛 %s', value => {
    const draft = task()
    draft.config!.loss_max_percent = value
    expect(validateTask(draft)).toEqual({ loss_max_percent: expect.any(String) })
  })
  it('延迟门槛须大于零；启用下载时名额不得超过对应预算', () => {
    const draft = task()
    draft.config!.latency_max_ms = 0
    draft.config!.ipv4_limit = 1
    expect(validateTask(draft)).toMatchObject({ latency_max_ms: expect.any(String), [DOWNLOAD4_FIELD]: expect.any(String) })
  })
  it.each(Array.from({ length: 8 }, (_, mask) => mask))('过滤启停组合 %s 只保存启用项目且下载后置', mask => {
    const config = { ...DEFAULT_CONFIG, latency_enabled: !!(mask & 1), loss_enabled: !!(mask & 2), download_enabled: !!(mask & 4), filter_order: ['loss', 'latency', 'download'] }
    const expected = ['loss', 'latency', 'download'].filter(step => step === 'loss' ? config.loss_enabled : step === 'latency' ? config.latency_enabled : config.download_enabled)
    expect(activeFilterOrder(config)).toEqual(expected)
    expect(validateTask({ ...task(), config: { ...config, filter_order: expected } })).toEqual({})
  })
  it('重新编辑部分过滤时可补回停用项目，已存在的非下载相对顺序保留', () => {
    expect(editableFilterOrder(['loss'])).toEqual(['loss', 'latency', 'download'])
    expect(editableFilterOrder([])).toEqual(['latency', 'loss', 'download'])
    expect(editableFilterOrder(['loss', 'download'])).toEqual(['loss', 'latency', 'download'])
  })
  it.each([
    { order: ['download', 'loss', 'latency'] }, { order: ['latency', 'latency', 'download'] },
    { order: ['latency', 'unknown', 'download'] }, { order: ['latency'] },
  ])('拒绝不合法过滤顺序 $order', ({ order }) => {
    const draft = task()
    draft.config!.filter_order = order
    expect(validateTask(draft).filter_order).toBeTruthy()
  })
  it.each(['ftp://example.test/file', 'not-a-url', 'https://user:secret@example.test/file', 'javascript:alert(1)'])('拒绝非 HTTP 或包含凭据的下载地址 %s', url => {
    const draft = task()
    draft.config!.download_url = url
    expect(validateTask(draft).download_url).toBeTruthy()
  })
  it('允许自定义 HTTP／HTTPS 且不访问下载地址', () => {
    const draft = task()
    draft.config!.download_url = 'http://192.0.2.2/sample.bin'
    expect(validateTask(draft).download_url).toBeUndefined()
  })
  it('预算与两个独立下载窗口使用正整数约束', () => {
    const draft = task()
    draft.config!.sample_size = 0
    draft.config!.prebody_timeout_seconds = 61
    draft.config!.download_window_seconds = 2.5
    expect(validateTask(draft)).toMatchObject({ sample_size: expect.any(String), prebody_timeout_seconds: expect.any(String), download_window_seconds: expect.any(String) })
  })
  it('地址族、DNS 自动目标、自动渠道引用分别验证', () => {
    const draft = task()
    draft.config!.ipv4_enabled = false
    draft.dns_auto = true
    draft.notifications = { telegram: { enabled: true, connection_id: '' } }
    expect(validateTask(draft)).toMatchObject({ ipv4_enabled: expect.any(String), targets: expect.any(String), telegram: expect.any(String) })
  })
  it('未知和未测不冒充数值 0；真实 0 仍然可见', () => {
    expect(metric(null, ' ms')).toBe('未测／未知')
    expect(metric(undefined)).toBe('未测／未知')
    expect(metric(0, '%')).toBe('0%')
    expect(metric(NaN)).toBe('未测／未知')
    expect(qualifiedCount({ id: 'r', task_id: 't' }, 'ipv4')).toBeUndefined()
    expect(qualifiedCount({ id: 'r', task_id: 't', result: { candidates: [] } }, 'ipv4')).toBe(0)
  })
  it('自动收尾占用席位时仍为活动执行', () => {
    expect(active({ id: 'r', task_id: 't', status: 'running', stage: 'notifications', slot_busy: true })).toBe(true)
  })
})

describe('日志条件与可读状态', () => {
  const log = { action: 'delivery_result', object: 'delivery-original', result: 'unconfirmed', at: '2026-11-14T09:30:00Z' }
  it('结果精确匹配，动作、对象及时间边界采用 AND', () => {
    expect(logMatches(log, new URLSearchParams({ action: 'delivery', object: 'original', result: 'unconfirmed', start: log.at, end: log.at }))).toBe(true)
    const mismatches: Record<string, string>[] = [{ action: 'run_finished' }, { object: 'other' }, { result: 'failed' }, { start: '2026-11-14T09:30:01Z' }, { end: '2026-11-14T09:29:59Z' }]
    for (const mismatch of mismatches) {
      expect(logMatches(log, new URLSearchParams({ action: 'delivery', object: 'original', result: 'unconfirmed', ...mismatch }))).toBe(false)
    }
  })
  it('前端对象字段映射为 object，保留 result 并转换为 RFC3339 时间', () => {
    const query = logQuery({ action: ' delivery ', object_id: ' original ', result: 'unconfirmed', start: '2026-11-14T09:00', end: '' }, 'UTC')
    expect(query).toMatchObject({ action: 'delivery', object: 'original', result: 'unconfirmed', end: '' })
    expect(query.start).toMatch(/T.*Z$/)
    expect(query).not.toHaveProperty('object_id')
  })
  it.each([
    ['cancellation_notifications', '取消通知收尾'], ['finalizing', '收尾处理中'],
    ['no_results', '无合格结果'], ['filtered', '已淘汰'], ['filtering', '过滤判定'],
  ])('状态 %s 有中文说明', (state, text) => { expect(label(state)).toBe(text) })
})

describe('AC-88～91 运行记录摘要', () => {
  function run(): Run {
    return {
      id: 'r', task_id: 't', status: 'completed', trigger: 'manual', queued_at: '2026-11-14T01:00:00Z',
      started_at: '2026-11-14T01:01:00Z', ended_at: '2026-11-14T01:03:30Z',
      snapshot: { ...task(), config: { ...DEFAULT_CONFIG, ipv6_enabled: true } },
      result: { status: 'completed', candidates: [
        { ip: '192.0.2.2', family: 'ipv4', eligible: true, rank: 2 },
        { ip: '2001:db8::1', family: 'ipv6', eligible: true, rank: 1, latency_ms: 70 },
        { ip: '192.0.2.1', family: 'ipv4', eligible: true, rank: 1, latency_ms: 30, loss_percent: 0, speed_mb_s: null },
      ] },
      deliveries: [
        { kind: 'dns', state: 'success' }, { kind: 'dns', state: 'failed' }, { kind: 'dns', state: 'unconfirmed' },
        { kind: 'telegram', state: 'unconfirmed' }, { kind: 'wecom', state: 'success' },
      ],
    }
  }
  it('七个主状态与当前管理 API 完全一致', () => {
    expect(RUN_STATUSES).toEqual(['queued', 'running', 'completed', 'no_results', 'failed', 'cancelled', 'interrupted'])
  })
  it('同任务的入队/状态/来源/DNS/通知条件独立后取 AND，DNS成功不掩盖失败', () => {
    const filters = new URLSearchParams({ start: '2026-11-14T01:00:00Z', end: '2026-11-14T01:00:00Z', status: 'completed', trigger: 'manual', dns_state: 'failed', notification_state: 'unconfirmed' })
    expect(runMatches(run(), filters)).toBe(true)
    filters.set('notification_state', 'failed')
    expect(runMatches(run(), filters)).toBe(false)
    filters.delete('notification_state')
    filters.set('trigger', 'scheduled')
    expect(runMatches(run(), filters)).toBe(false)
  })
  it('未实际开始就取消的记录仍按入队时间命中', () => {
    const item = { ...run(), status: 'cancelled', started_at: undefined }
    expect(runMatches(item, new URLSearchParams({ start: item.queued_at!, end: item.queued_at!, status: 'cancelled' }))).toBe(true)
    expect(duration(item)).toContain('未实际开始')
  })
  it('耗时使用实际开始到收尾结束；缺失时间不伪造0', () => {
    expect(duration(run())).toBe('2 分 30 秒（含收尾）')
    expect(duration({ ...run(), ended_at: undefined })).toBe('未知')
    expect(duration({ ...run(), started_at: undefined })).not.toContain('0 分')
  })
  it('各族合格数与最终第一名独立，不能依赖候选数组顺序', () => {
    expect(familyResult(run(), 'ipv4')).toMatchObject({ state: 'ready', count: 2, best: { ip: '192.0.2.1' } })
    expect(familyResult(run(), 'ipv6')).toMatchObject({ state: 'ready', count: 1, best: { ip: '2001:db8::1' } })
    expect(familyResultText(familyResult(run(), 'ipv6'))).toBe('合格 1')
    expect(metric(familyResult(run(), 'ipv4').best?.speed_mb_s)).toBe('未测／未知')
  })
  it('未启用、未产生、无合格、未知四种情况不混为0', () => {
    const item = run()
    item.snapshot!.config!.ipv6_enabled = false
    expect(familyResultText(familyResult(item, 'ipv6'))).toBe('未启用')
    expect(familyResultText(familyResult({ ...run(), status: 'running', result: { candidates: [] } }, 'ipv4'))).toBe('未产生')
    expect(familyResultText(familyResult({ ...run(), result: { status: 'no_results', candidates: [] } }, 'ipv4'))).toBe('合格 0（无合格结果）')
    expect(familyResultText(familyResult({ ...run(), result: undefined }, 'ipv4'))).toBe('未知')
  })
  it('没有已知最终名次时不把数组首项伪造成第一名', () => {
    const item = run()
    item.result!.candidates!.forEach(candidate => { delete candidate.rank })
    expect(familyResult(item, 'ipv4')).toMatchObject({ state: 'ready', count: 2 })
    expect(familyResult(item, 'ipv4').best).toBeUndefined()
  })
  it('DNS混合结果保留所有异常，两个通知渠道分别呈现', () => {
    expect(deliverySummary(run(), 'dns')).toBe('成功、失败、结果未确认')
    expect(deliverySummary(run(), 'telegram')).toBe('结果未确认')
    expect(deliverySummary(run(), 'wecom')).toBe('成功')
  })
  it('恢复期只从删除时间加7天计算，不误用运行保留期或旧字段', () => {
    const item = { ...run(), deleted_at: '2026-11-14T01:00:00Z', retention_at: '2027-01-01T00:00:00Z', restore_until: '2028-01-01T00:00:00Z' }
    expect(restoreDeadline(item)).toBe('2026-11-21T01:00:00.000Z')
    expect(restoreDeadline({ ...item, deleted_at: undefined })).toBeUndefined()
    expect(restoreDeadline({ ...item, deleted_at: 'bad-time' })).toBeUndefined()
  })
})

describe('DNS 范围与序列化', () => {
  const candidates: Candidate[] = [
    { ip: '192.0.2.1', family: 'ipv4', eligible: true },
    { ip: '192.0.2.2', family: 'ipv4', eligible: false },
    { ip: '2001:db8::1', family: 'ipv6', eligible: true },
    { ip: '192.0.2.3', family: 'ipv4' },
  ]
  const target: Target = { connection_id: 'c', account_id: 'a', record_id: 'record', type: 'A' }
  it('A 只可匹配明确最终合格 IPv4', () => { expect(matchingCandidates(candidates, target).map(item => item.ip)).toEqual(['192.0.2.1']) })
  it('AAAA 只可匹配 IPv6，不从任意字符串构造地址', () => { expect(matchingCandidates(candidates, { ...target, type: 'AAAA' }).map(item => item.ip)).toEqual(['2001:db8::1']) })
  it('其他记录类型不能投递', () => { expect(matchingCandidates(candidates, { ...target, type: 'TXT' })).toEqual([]) })
  it('同账号记录的保护身份不按连接别名拆分', () => { expect(targetKey(target)).toBe(targetKey({ ...target, connection_id: 'alias' })) })
  it('能从 Vue 嵌套代理生成可提交 DTO，不触发 DataCloneError', () => {
    const draft = reactive(task())
    const copy = cloneDto(draft)
    expect(copy.config).toEqual(DEFAULT_CONFIG)
    copy.name = '另一个名字'
    expect(draft.name).toBe('本地测试任务')
  })
  it('datetime-local 转为包含时区的 RFC3339', () => {
    const result = timeQuery({ start: '2026-11-14T09:00', end: '', status: 'completed' }, 'UTC')
    expect(result.start).toMatch(/T[0-9:]+[.][0-9]+Z$/)
    expect(result.end).toBe('')
    expect(result.status).toBe('completed')
  })
})
