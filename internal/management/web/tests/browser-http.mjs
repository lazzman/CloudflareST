import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const root = fileURLToPath(new URL('../', import.meta.url))
const server = await createServer({ root, mode: 'mock', logLevel: 'error', server: { host: '127.0.0.1', port: 5196, strictPort: false, hmr: false } })
let browser
try {
  await server.listen()
  const address = server.httpServer.address()
  assert(address && typeof address === 'object')
  const localOrigin = 'http://127.0.0.1:' + address.port
  browser = await chromium.launch({ headless: true, args: ['--disable-background-networking', '--no-proxy-server'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) })
  for (const origin of [localOrigin, 'http://192.0.2.1']) {
    const context = await browser.newContext({ serviceWorkers: 'block' })
    await context.route('**/*', async route => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort()
      const response = await route.fetch({ url: localOrigin + url.pathname + url.search, maxRedirects: 0 })
      return route.fulfill({ response })
    })
    const page = await context.newPage()
    page.setDefaultTimeout(10000)
    await page.goto(origin + '/#/login')
    await page.getByLabel('管理密码').fill('仅模拟登录')
    await page.getByRole('button', { name: '登录管理端' }).click()
    await page.waitForURL('**/#/tasks')
    await page.goto(origin + '/#/connections')
    await page.getByRole('heading', { name: '服务连接', exact: true }).waitFor()
    const capabilities = await page.evaluate(() => ({ secure: isSecureContext, digest: typeof crypto.subtle?.digest === 'function', uuid: typeof crypto.randomUUID === 'function', random: typeof crypto.getRandomValues === 'function' }))
    assert.deepEqual(capabilities, { secure: origin === localOrigin, digest: origin === localOrigin, uuid: origin === localOrigin, random: true })
    await page.getByText('当前使用 HTTP，密码与凭据传输未加密；仅建议在可信网络使用。', { exact: true }).waitFor()
    await page.getByRole('button', { name: '新增连接', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: '新增连接' })
    await dialog.getByLabel('连接名称').fill('HTTP 兼容测试')
    await dialog.getByLabel('AccessKey ID', { exact: true }).fill('FAKE_HTTP_ID')
    await dialog.getByLabel('AccessKey Secret', { exact: true }).fill('FAKE_HTTP_SECRET')
    await dialog.getByRole('button', { name: '保存连接', exact: true }).click()
    await dialog.waitFor({ state: 'detached' })
    const row = page.getByRole('row').filter({ hasText: 'HTTP 兼容测试' })
    await row.waitFor()
    await row.getByText('查看凭据', { exact: true }).click()
    assert((await row.innerText()).includes('FAKE_HTTP_SECRET'))
    const verification = await page.evaluate(async () => {
      const client = await import('/src/api/client.ts')
      const mock = await import('/src/api/mock.ts')
      await client.write('/settings', { method: 'PATCH', body: { timezone: 'Asia/Shanghai', history_days: 30 } })
      let writes = 0
      const ids = []
      const unknown = () => new Response(JSON.stringify({ code: 'operation_unknown', message: '查不到原号' }), { status: 404, headers: { 'Content-Type': 'application/json' } })
      client.setTransport(async (input, init) => {
        if (String(input).startsWith('/api/v1/operations/')) return unknown()
        if (input === '/api/v1/connections' && init?.method === 'POST') {
          writes++
          ids.push(JSON.parse(String(init.body)).operation_id)
          throw new Error('仅测试响应丢失')
        }
        return mock.mockFetch(input, init)
      })
      let originalId
      let firstCode
      const body = { connection: { kind: 'telegram', name: '仅测试原号查询', secrets: { bot_token: 'FAKE_HTTP_TOKEN', chat_id: 'FAKE_HTTP_CHAT' } } }
      try { await client.write('/connections', { body }) }
      catch (error) { originalId = error.details?.operation_id; firstCode = error.details?.code }
      let secondCode
      try { await client.write('/connections', { body }) }
      catch (error) { secondCode = error.details?.code }
      const stored = localStorage.getItem(client.OPERATION_STORAGE_KEY) || ''
      const pending = client.getPendingIds()
      client.setTransport(async input => String(input) === '/api/v1/operations/' + originalId
        ? new Response(JSON.stringify({ operation_id: originalId, state: 'completed' }), { headers: { 'Content-Type': 'application/json' } })
        : new Response(JSON.stringify({ code: 'NOT_FOUND' }), { status: 404, headers: { 'Content-Type': 'application/json' } }))
      await client.lookupOperation(originalId)
      return { writes, ids, originalId, firstCode, secondCode, leaked: stored.includes('FAKE_HTTP') || stored.includes('FAKE_CHANGED'), pending, afterLookup: client.getPendingIds() }
    })
    assert.equal(verification.writes, 2)
    assert.match(verification.originalId, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    assert.deepEqual(verification.ids, [verification.originalId, verification.originalId])
    assert.equal(verification.firstCode, 'OPERATION_NOT_FOUND')
    assert.equal(verification.secondCode, 'OPERATION_NOT_FOUND')
    assert.equal(verification.leaked, false)
    assert.deepEqual(verification.pending, [verification.originalId])
    assert.deepEqual(verification.afterLookup, [])
    console.log((origin === localOrigin ? '本机 HTTP' : '非本机 IP HTTP') + '：连接保存、配置写入、同号重试和原号查询通过；仅使用模拟凭据')
    await context.close()
  }
} finally {
  await browser?.close()
  await server.close()
}
