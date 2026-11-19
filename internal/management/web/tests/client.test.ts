import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, OPERATION_STORAGE_KEY, apiPath, forgetOperation, getPendingIds, getPendingOperations, lookupOperation, newOperationId, request, restoreOperationIds, setCsrf, setTransport, setUnauthorizedHandler, write } from '../src/api/client'
import { useMutation } from '../src/lib/useMutation'
const saved = new Map<string, string>()
beforeEach(() => {
  saved.clear()
  vi.stubGlobal('localStorage', { getItem: (key: string) => saved.get(key) || null, setItem: (key: string, value: string) => { saved.set(key, value) } })
  getPendingIds().forEach(forgetOperation)
  saved.clear()
  setCsrf('csrf-test-value')
  setUnauthorizedHandler(() => {})
})
afterEach(() => { getPendingIds().forEach(forgetOperation); vi.unstubAllGlobals(); vi.restoreAllMocks() })
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })

async function failThenLookup(state: string, status = 200) {
  const posts: { id: string; body: Record<string, unknown> }[] = []
  const gets: string[] = []
  setTransport(async (input, init) => {
    if ((init?.method || 'GET') === 'GET') {
      gets.push(String(input))
      return status === 404 ? json({ code: 'operation_unknown', message: '查不到原号' }, 404) : json({ operation_id: posts[0]?.id, state })
    }
    const body = JSON.parse(String(init?.body)) as Record<string, unknown>
    posts.push({ id: String(body.operation_id), body })
    throw new TypeError('模拟响应丢失')
  })
  return { posts, gets }
}

describe('同源契约与操作收据', () => {
  it.each(['subtle', 'digest', 'randomUUID', 'both'])('缺少 %s 时兼容 HTTP 环境保存连接', async missing => {
    const nativeCrypto = crypto
    vi.stubGlobal('crypto', {
      subtle: ['subtle', 'both'].includes(missing) ? undefined : { digest: missing === 'digest' ? undefined : nativeCrypto.subtle.digest.bind(nativeCrypto.subtle) },
      randomUUID: ['randomUUID', 'both'].includes(missing) ? undefined : nativeCrypto.randomUUID.bind(nativeCrypto),
      getRandomValues: nativeCrypto.getRandomValues.bind(nativeCrypto),
    })
    const transport = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body))
      expect(body.operation_id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
      expect(new Headers(init?.headers).get('Idempotency-Key')).toBe(body.operation_id)
      return json({ operation_id: body.operation_id, state: 'completed' }, 202)
    })
    setTransport(transport)
    const mutation = useMutation()
    await mutation.submit('/connections', { body: { connection: { name: '仅测试连接' } } })
    expect(mutation.error.value).toBeUndefined()
    expect(mutation.receipt.value?.state).toBe('completed')
    expect(transport).toHaveBeenCalledOnce()
    expect(getPendingIds()).toEqual([])
  })
  it.each(['crypto', 'getRandomValues'])('缺少 %s 且无原生 UUID 时明确说明未提交', async missing => {
    vi.stubGlobal('crypto', missing === 'crypto' ? undefined : {})
    const transport = vi.fn()
    setTransport(transport)
    const mutation = useMutation()
    await mutation.submit('/connections', { body: { connection: { name: '仅测试连接' } } })
    expect(mutation.error.value).toMatchObject({ uncertain: false, details: { code: 'BROWSER_RANDOM_UNAVAILABLE', message: expect.stringContaining('本次未提交') } })
    expect(mutation.error.value?.details.operation_id).toBeUndefined()
    expect(mutation.busy.value).toBe(false)
    expect(transport).not.toHaveBeenCalled()
    expect(getPendingIds()).toEqual([])
    expect(saved.size).toBe(0)
  })
  it('兼容 UUID 使用安全随机数并正确设置版本和变体位', () => {
    const getRandomValues = vi.fn((bytes: Uint8Array) => bytes.fill(255))
    vi.stubGlobal('crypto', { getRandomValues })
    expect(newOperationId()).toBe('ffffffff-ffff-4fff-bfff-ffffffffffff')
    expect(getRandomValues).toHaveBeenCalledOnce()
  })
  it('读取直接实体，不解包不存在的 data', async () => {
    setTransport(async (input, init) => { expect(input).toBe('/api/v1/defaults'); expect(init?.credentials).toBe('same-origin'); expect(init?.redirect).toBe('error'); return json({ timezone: 'Asia/Shanghai' }) })
    await expect(request('/defaults')).resolves.toEqual({ timezone: 'Asia/Shanghai' })
  })
  it.each(['https://example.test', '//example.test', '/../outside', '/tasks#fragment'])('拒绝非 API 同源路径 %s', path => { expect(() => apiPath(path)).toThrow() })
  it('提交前生成 UUID，JSON 携带 operation_id，PATCH 使用 If-Match 和 CSRF', async () => {
    setTransport(async (input, init) => {
      const body = JSON.parse(String(init?.body))
      const headers = new Headers(init?.headers)
      expect(input).toBe('/api/v1/tasks/task-test')
      expect(headers.get('X-CSRF-Token')).toBe('csrf-test-value')
      expect(headers.get('If-Match')).toBe('7')
      expect(headers.get('Idempotency-Key')).toBe(body.operation_id)
      expect(getPendingIds()).toContain(body.operation_id)
      expect(body.task.name).toBe('示例任务')
      expect([...saved.keys()]).toEqual([OPERATION_STORAGE_KEY])
      expect(saved.get(OPERATION_STORAGE_KEY)).not.toContain('示例任务')
      return json({ operation_id: body.operation_id, object_id: 'task-test', state: 'completed' }, 202)
    })
    await expect(write('/tasks/task-test', { method: 'PATCH', revision: 7, body: { task: { name: '示例任务' } } })).resolves.toMatchObject({ object_id: 'task-test' })
    expect(getPendingIds()).toEqual([])
  })
  it('DELETE 用 Idempotency-Key，不提交 JSON 正文', async () => {
    setTransport(async (_input, init) => {
      const id = new Headers(init?.headers).get('Idempotency-Key')
      expect(id).toBeTruthy()
      expect(init?.body).toBeUndefined()
      return json({ operation_id: id, state: 'completed' }, 202)
    })
    await write('/runs/run-test', { method: 'DELETE' })
  })
  it('响应丢失后自动读取原号收据，确认后不再重交', async () => {
    let posts = 0
    let operationId = ''
    setTransport(async (input, init) => {
      if ((init?.method || 'GET') === 'GET') {
        expect(input).toBe('/api/v1/operations/' + operationId)
        return json({ operation_id: operationId, state: 'completed' })
      }
      posts++
      operationId = JSON.parse(String(init?.body)).operation_id
      throw new TypeError('模拟响应丢失')
    })
    const receipt = await write('/tasks/task-test', { method: 'PATCH', revision: 3, body: { task: { name: '示例' } } })
    expect(receipt.state).toBe('completed')
    expect(posts).toBe(1)
    expect(getPendingIds()).toEqual([])
  })
  it('收据仍未知时保留原号，再次提交复用原号而不是换号', async () => {
    const { posts, gets } = await failThenLookup('unknown', 404)
    await expect(write('/connections', { body: { connection: { name: '仅测试', secrets: { bot_token: 'FAKE_SECRET' } } } })).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    const original = getPendingIds()[0]!
    expect(posts[0]?.id).toBe(original)
    expect(gets).toEqual(['/api/v1/operations/' + original])
    await expect(write('/connections', { body: { connection: { name: '仅测试', secrets: { bot_token: 'FAKE_SECRET' } } } })).rejects.toMatchObject({ details: { operation_id: original } })
    expect(posts.map(item => item.id)).toEqual([original, original])
    expect(gets).toHaveLength(3)
    expect(gets.every(path => path === '/api/v1/operations/' + original)).toBe(true)
    expect(getPendingIds()).toEqual([original])
    expect(saved.get(OPERATION_STORAGE_KEY)).not.toContain('FAKE_SECRET')
  })
  it('不同连接内容使用不同操作号，不把新内容套到旧号上', async () => {
    const { posts } = await failThenLookup('unknown', 404)
    await expect(write('/connections', { body: { connection: { kind: 'telegram', name: '连接一', secrets: { bot_token: 'FAKE_A' } } } })).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    const first = getPendingIds()[0]!
    await expect(write('/connections', { body: { connection: { kind: 'telegram', name: '连接二', secrets: { bot_token: 'FAKE_B' } } } })).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    expect(getPendingIds()).toHaveLength(2)
    expect(getPendingIds()).toContain(first)
    expect(new Set(posts.map(item => item.id)).size).toBe(2)
  })
  it.each(['unknown', 'expired', 'unconfirmed', 'pending', 'retired'])('收据状态 %s 时保留待确认，不重交原动作', async state => {
    const { posts, gets } = await failThenLookup(state)
    await expect(write('/tasks/task-test/runs')).rejects.toMatchObject({ details: { code: 'OPERATION_UNCONFIRMED' } })
    expect(posts).toHaveLength(1)
    expect(gets).toHaveLength(1)
    expect(getPendingIds()).toHaveLength(1)
  })
  it('字段校验错误是确定失败，不查询也不保留待确认', async () => {
    setTransport(async () => json({ code: 'CONFIG_INVALID', message: '配置不合法', fields: { download_url: '请输入 HTTP 地址' } }, 422))
    await expect(write('/tasks')).rejects.toMatchObject({ details: { fields: { download_url: '请输入 HTTP 地址' } } })
    expect(getPendingIds()).toEqual([])
  })
  it('同一动作同时提交时沿用同一个操作号', async () => {
    let release: (() => void) | undefined
    const gate = new Promise<void>(resolve => { release = resolve })
    const ids: string[] = []
    setTransport(async (_input, init) => {
      if ((init?.method || 'GET') === 'GET') return json({ code: 'operation_unknown' }, 404)
      const body = JSON.parse(String(init?.body))
      ids.push(body.operation_id)
      await gate
      return json({ operation_id: body.operation_id, state: 'accepted' }, 202)
    })
    const first = write('/tasks/task-concurrent/runs')
    await vi.waitFor(() => { expect(ids).toHaveLength(1) })
    const second = write('/tasks/task-concurrent/runs')
    await vi.waitFor(() => { expect(ids).toHaveLength(2) })
    release?.()
    await expect(first).resolves.toMatchObject({ state: 'accepted' })
    await expect(second).resolves.toMatchObject({ state: 'accepted' })
    expect(new Set(ids).size).toBe(1)
  })
  it('无关动作不被待确认操作阻塞', async () => {
    const { posts } = await failThenLookup('unknown', 404)
    await expect(write('/tasks/task-one/runs')).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    const pending = getPendingIds()[0]!
    setTransport(async (_input, init) => {
      const body = JSON.parse(String(init?.body))
      return json({ operation_id: body.operation_id, state: 'accepted' }, 202)
    })
    await expect(write('/tasks/task-two/runs')).resolves.toMatchObject({ state: 'accepted' })
    await expect(write('/settings', { method: 'PATCH', body: { timezone: 'UTC' } })).resolves.toMatchObject({ state: 'accepted' })
    expect(getPendingIds()).toEqual([pending])
    expect(posts).toHaveLength(1)
  })
  it('按任务范围筛选待确认操作，避免串到其他任务', async () => {
    await failThenLookup('unknown', 404)
    await expect(write('/tasks/task-1/runs')).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    const id = getPendingIds()[0]!
    expect(getPendingOperations('/api/v1/tasks/task-1')).toEqual([{ id, scope: { method: 'POST', path: '/api/v1/tasks/task-1/runs' } }])
    expect(getPendingOperations('/api/v1/tasks/task-10')).toEqual([])
    expect(getPendingOperations('/api/v1/tasks', true)).toEqual([])
  })
  it('刷新后只恢复带动作范围的待确认记录', () => {
    const scoped = crypto.randomUUID()
    const legacy = crypto.randomUUID()
    saved.set(OPERATION_STORAGE_KEY, JSON.stringify({ version: 2, operations: [
      { operation_id: scoped, scope: { method: 'PATCH', path: '/api/v1/tasks/task-1' } },
      { operation_id: legacy },
    ] }))
    restoreOperationIds()
    expect(getPendingIds()).toEqual([scoped])
    expect(getPendingOperations('/api/v1/tasks/task-1')).toEqual([{ id: scoped, scope: { method: 'PATCH', path: '/api/v1/tasks/task-1' } }])
  })
  it('恢复带指纹的旧记录后，同动作读取收据确认并清理', async () => {
    const body = { task: { name: '刷新恢复测试', config: { sample_size: 20 } } }
    await failThenLookup('unknown', 404)
    await expect(write('/tasks', { body })).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    const original = getPendingIds()[0]!
    vi.resetModules()
    const restored = await import('../src/api/client')
    restored.restoreOperationIds()
    restored.setTransport(async (_input, init) => {
      if ((init?.method || 'GET') === 'GET') return json({ operation_id: original, state: 'completed', object_id: 'task-created' })
      throw new Error('不应再次提交')
    })
    await expect(restored.write('/tasks', { body })).resolves.toMatchObject({ object_id: 'task-created' })
    expect(restored.getPendingIds()).toEqual([])
  })
  it('凭据动作只摘要非敏感范围，不持久化秘密', async () => {
    const credential = '仅测试内存-' + crypto.randomUUID()
    const digest = vi.spyOn(crypto.subtle, 'digest')
    await failThenLookup('unknown', 404)
    await expect(write('/connections', { revision: 7, body: { connection: { name: '不应持久化的显示名', secrets: { token: credential } } } })).rejects.toMatchObject({ details: { code: 'OPERATION_NOT_FOUND' } })
    expect(JSON.parse(new TextDecoder().decode(digest.mock.calls[0]![1] as ArrayBuffer))).toEqual({ path: '/api/v1/connections', method: 'POST', body: { connection: { name: '不应持久化的显示名', secret_keys: ['token'] } } })
    expect(saved.get(OPERATION_STORAGE_KEY)).not.toContain(credential)
    expect(saved.get(OPERATION_STORAGE_KEY)).not.toContain('不应持久化的显示名')
  })
  it('401 调用隐藏会话处理，不重交请求', async () => {
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)
    setTransport(async () => json({ code: 'AUTH_REQUIRED', message: '请登录' }, 401))
    await expect(request('/tasks')).rejects.toMatchObject({ status: 401 })
    expect(unauthorized).toHaveBeenCalledOnce()
  })
  it('有效收据查询后清理待确认，不会重提动作', async () => {
    await failThenLookup('unknown', 404)
    await expect(write('/tasks/task-test/runs')).rejects.toBeInstanceOf(ApiError)
    const id = getPendingIds()[0]!
    setTransport(async () => json({ operation_id: id, object_id: 'run-test', state: 'completed' }))
    await expect(lookupOperation(id)).resolves.toMatchObject({ object_id: 'run-test' })
    expect(getPendingIds()).toEqual([])
  })
})
