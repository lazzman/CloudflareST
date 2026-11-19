import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { computed } from 'vue'
import { setTransport } from '../src/api/client'
import { date as formatDate } from '../src/lib/domain'
import { instantToSystemInput, systemTimeToISO } from '../src/lib/datetime'
import { clearSettings, date, loadSettings, systemTimezone } from '../src/lib/settings'
import { timeQuery } from '../src/lib/serialize'
import { logQuery } from '../src/lib/logs'

beforeEach(clearSettings)
afterEach(() => { clearSettings(); vi.unstubAllEnvs() })
const json = (value: unknown) => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } })

describe('系统时区显示与时间筛选', () => {
  it('UTC 与上海显示同一时刻，不修改原始时间', () => {
    const instant = '2027-01-01T00:00:00Z'
    expect(formatDate(instant, 'UTC')).toBe('2027/1/1 00:00:00')
    expect(formatDate(instant, 'Asia/Shanghai')).toBe('2027/1/1 08:00:00')
    expect(instant).toBe('2027-01-01T00:00:00Z')
    expect(formatDate(undefined, 'UTC')).toBe('未知')
    expect(formatDate('不是日期', 'UTC')).toBe('未知')
    expect(formatDate(instant, undefined)).toBe(instant + '（系统时区未知）')
    expect(formatDate(instant, 'not-a-zone')).toBe(instant + '（系统时区无效）')
  })
  it('后端设置读取与切换响应式更新所有共享格式化调用，读取失败保留已知时区', async () => {
    const rendered = computed(() => date('2027-01-01T00:00:00Z'))
    expect(rendered.value).toContain('系统时区未知')
    setTransport(async () => json({ timezone: 'UTC' }))
    await loadSettings()
    expect(rendered.value).toBe('2027/1/1 00:00:00')
    setTransport(async () => json({ timezone: 'Asia/Shanghai' }))
    await loadSettings()
    expect(rendered.value).toBe('2027/1/1 08:00:00')
    setTransport(async () => { throw new Error('断网') })
    await expect(loadSettings()).rejects.toThrow()
    expect(systemTimezone.value).toBe('Asia/Shanghai')
    clearSettings()
    expect(rendered.value).toContain('系统时区未知')
  })
  it('较早发出的设置读取不能覆盖新时区，退出后迟到响应不能恢复旧状态', async () => {
    let release: ((value: Response) => void) | undefined
    setTransport(() => new Promise(resolve => { release = resolve }))
    const previous = loadSettings()
    setTransport(async () => json({ timezone: 'UTC' }))
    await loadSettings()
    release!(json({ timezone: 'Asia/Shanghai' }))
    await previous
    expect(systemTimezone.value).toBe('UTC')
    setTransport(() => new Promise(resolve => { release = resolve }))
    const late = loadSettings()
    clearSettings()
    release!(json({ timezone: 'Asia/Shanghai' }))
    await late
    expect(systemTimezone.value).toBeUndefined()
  })
  it.each(['America/Los_Angeles', 'UTC', 'Asia/Tokyo'])('浏览器/进程本地时区 %s 不影响系统时区筛选', timezone => {
    vi.stubEnv('TZ', timezone)
    expect(systemTimeToISO('2027-01-01T00:00', 'UTC')).toBe('2027-01-01T00:00:00.000Z')
    expect(systemTimeToISO('2027-01-01T08:00', 'Asia/Shanghai')).toBe('2027-01-01T00:00:00.000Z')
    expect(systemTimeToISO('2027-01-01T05:45', 'Asia/Kathmandu')).toBe('2027-01-01T00:00:00.000Z')
    expect(systemTimeToISO('2027-01-01T08:00:12.345', 'Asia/Shanghai')).toBe('2027-01-01T00:00:12.345Z')
    expect(formatDate('2027-01-01T00:00:00Z', 'UTC')).toBe('2027/1/1 00:00:00')
  })
  it.each([
    ['2026-01-15T09:00', 'America/New_York', '2026-01-15T14:00:00.000Z'],
    ['2026-07-15T09:00', 'America/New_York', '2026-07-15T13:00:00.000Z'],
    ['2026-03-08T03:00', 'America/New_York', '2026-03-08T07:00:00.000Z'],
    ['2026-11-01T02:00', 'America/New_York', '2026-11-01T07:00:00.000Z'],
    ['2026-04-05T02:00', 'Australia/Lord_Howe', '2026-04-04T15:30:00.000Z'],
  ])('正确换算季节性偏移 %s / %s', (value, timezone, expected) => {
    expect(systemTimeToISO(value, timezone)).toBe(expected)
  })
  it.each([
    ['2026-03-08T02:30', 'America/New_York', '不存在'],
    ['2026-11-01T01:30', 'America/New_York', '重复'],
    ['2026-04-05T01:45', 'Australia/Lord_Howe', '重复'],
    ['2026-10-04T02:15', 'Australia/Lord_Howe', '不存在'],
  ])('拒绝 DST 无效或歧义时间 %s / %s', (value, timezone, message) => {
    expect(() => systemTimeToISO(value, timezone)).toThrow(message)
  })
  it.each(['2026-02-30T12:00', '2026-01-01T24:00', '2026-01-01T09:60', 'invalid', '2026-01-01T09:00Z'])('拒绝无效本地时间 %s', value => {
    expect(() => systemTimeToISO(value, 'UTC')).toThrow()
  })
  it('未读取或无效系统时区不回退为本机时区，空筛选无需转换', () => {
    expect(() => systemTimeToISO('2027-01-01T00:00', undefined)).toThrow('尚未读取')
    expect(() => systemTimeToISO('2027-01-01T00:00', 'bad-zone')).toThrow('系统时区无效')
    expect(timeQuery({ start: '', end: '', status: 'completed' }, undefined)).toEqual({ start: '', end: '', status: 'completed' })
  })
  it('结束时间按所选分钟包含整分钟，显式秒值保持原样', () => {
    expect(timeQuery({ start: '', end: '2027-01-01T08:00' }, 'Asia/Shanghai').end).toBe('2027-01-01T00:00:59.999Z')
    expect(timeQuery({ start: '', end: '2027-01-01T08:00:12.345' }, 'Asia/Shanghai').end).toBe('2027-01-01T00:00:12.345Z')
  })
  it('把瞬间格式化为系统时区的本地输入值', () => {
    expect(instantToSystemInput(Date.parse('2027-01-01T00:00:00Z'), 'Asia/Shanghai')).toBe('2027-01-01T08:00')
    expect(instantToSystemInput(Date.parse('2027-01-01T00:00:00Z'), 'UTC')).toBe('2027-01-01T00:00')
    expect(() => instantToSystemInput(Date.now(), undefined)).toThrow('尚未读取')
    expect(() => instantToSystemInput(Date.now(), 'bad-zone')).toThrow('系统时区无效')
  })
  it('运行与日志筛选显式采用同一系统时区，不修改表单字段', () => {
    const filters = { action: ' delivery ', actor: '系统', object_id: ' original ', result: 'unconfirmed', start: '2027-01-01T08:00', end: '2027-01-01T09:00' }
    const runs = timeQuery(filters, 'Asia/Shanghai')
    const logs = logQuery(filters, 'Asia/Shanghai')
    expect(logs).toMatchObject({ start: '2027-01-01T00:00:00.000Z', end: '2027-01-01T01:00:59.999Z', action: 'delivery', actor: '系统', object: 'original' })
    expect(runs.start).toBe(logs.start)
    expect(runs.end).toBe(logs.end)
    expect(filters.start).toBe('2027-01-01T08:00')
  })
})
