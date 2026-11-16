import { beforeAll, describe, expect, it } from 'vitest'
import { mockFetch } from '../src/api/mock'
import { DEFAULT_CONFIG } from '../src/lib/domain'
import type { Task } from '../src/api/types'

beforeAll(async () => { await mockFetch('/api/v1/session', { method: 'POST', body: JSON.stringify({ password: '仅本地测试，不是真实凭据' }) }) })
describe('显式预览适配器', () => {
  it('拒绝请求外部域名，不回退到真实 fetch', async () => { expect((await mockFetch('https://example.test/api/v1/tasks')).status).toBe(403) })
  it('提供 R3 直接数组与独立字段样例', async () => {
    const tasks = await (await mockFetch('/api/v1/tasks')).json() as Task[]
    expect(Array.isArray(tasks)).toBe(true)
    expect(tasks[0]?.config?.download_url).toBe(DEFAULT_CONFIG.download_url)
  })
  it('任务保存使用 task 嵌套对象；版本冲突拒绝覆盖', async () => {
    const created = await mockFetch('/api/v1/tasks', { method: 'POST', body: JSON.stringify({ operation_id: crypto.randomUUID(), task: { name: '契约测试任务', config: DEFAULT_CONFIG } }) })
    const receipt = await created.json()
    const result = await (await mockFetch('/api/v1/tasks/' + receipt.object_id)).json()
    expect(result.name).toBe('契约测试任务')
    expect(result.config.download_url).toBe(DEFAULT_CONFIG.download_url)
    const conflict = await mockFetch('/api/v1/tasks/' + receipt.object_id, { method: 'PATCH', headers: { 'If-Match': '0' }, body: JSON.stringify({ operation_id: crypto.randomUUID(), task: { ...result, name: '不能覆盖' } }) })
    expect(conflict.status).toBe(409)
  })
  it('不存在的操作号返回 unknown，不能伪造成失败或从未执行', async () => {
    const id = crypto.randomUUID()
    const result = await (await mockFetch('/api/v1/operations/' + id)).json()
    expect(result).toEqual({ operation_id: id, state: 'unknown' })
  })
  it('编辑连接仅改名时省略 secrets，凭据状态与版本仍保留', async () => {
    const list = await (await mockFetch('/api/v1/connections')).json()
    const connection = list.find((item: { kind: string }) => item.kind === 'wecom')
    const result = await mockFetch('/api/v1/connections/' + connection.id, { method: 'PATCH', headers: { 'If-Match': String(connection.revision) }, body: JSON.stringify({ operation_id: crypto.randomUUID(), connection: { kind: 'wecom', name: '仅改显示名' } }) })
    expect(result.status).toBe(202)
    const updated = await (await mockFetch('/api/v1/connections')).json()
    expect(updated.find((item: { id: string }) => item.id === connection.id)).toMatchObject({ name: '仅改显示名', credentials_set: true, revision: connection.revision + 1 })
    expect(JSON.stringify(updated)).not.toContain('secrets')
  })
  it('首次发送不能绕过已有失败渠道，重试仅接受原投递 ID 而非 attempt_id', async () => {
    const send = (channels: unknown[]) => mockFetch('/api/v1/runs/run-demo-001/notifications', { method: 'POST', body: JSON.stringify({ operation_id: crypto.randomUUID(), risk_acknowledged: true, channels }) })
    expect((await send([{ kind: 'telegram', connection_id: 'connection-telegram' }])).status).toBe(409)
    expect((await send([{ kind: 'telegram', connection_id: 'connection-telegram', prior_attempt_id: 'attempt-demo-failed' }])).status).toBe(409)
    expect((await send([{ kind: 'telegram', connection_id: 'connection-telegram', prior_attempt_id: 'delivery-demo-failed' }])).status).toBe(202)
    const run = await (await mockFetch('/api/v1/tasks/task-demo/runs/run-demo-001')).json()
    const latest = run.deliveries.at(-1)
    expect(latest).toMatchObject({ kind: 'telegram', prior_attempt_id: 'delivery-demo-failed', state: 'pending' })
    expect(latest.operation_id).toBeTruthy()
  })
  it('日志结果与动作、对象共同过滤，不退化为 OR', async () => {
    const list = await (await mockFetch('/api/v1/operation-logs')).json()
    const entry = list.find((item: { action: string }) => item.action === 'mock_action')
    const filters = new URLSearchParams({ action: entry.action, object: entry.object_id, result: entry.state })
    const matched = await (await mockFetch('/api/v1/operation-logs?' + filters)).json()
    expect(matched.length).toBeGreaterThan(0)
    expect(matched.every((item: { object_id: string }) => item.object_id === entry.object_id)).toBe(true)
    filters.set('result', 'failed')
    expect(await (await mockFetch('/api/v1/operation-logs?' + filters)).json()).toEqual([])
  })
})
