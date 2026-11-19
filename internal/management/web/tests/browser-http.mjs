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
    const dialog = page.getByRole('dialog', { name: '新增共享连接' })
    await dialog.getByLabel('连接名称').fill('HTTP 兼容测试')
    await dialog.getByLabel('AccessKey ID', { exact: true }).fill('FAKE_HTTP_ID')
    await dialog.getByLabel('AccessKey Secret', { exact: true }).fill('FAKE_HTTP_SECRET')
    await dialog.getByRole('button', { name: '保存连接', exact: true }).click()
    await dialog.waitFor({ state: 'detached' })
    const row = page.getByRole('row').filter({ hasText: 'HTTP 兼容测试' })
    await row.waitFor()
    assert((await row.innerText()).includes('FAKE_HTTP_SECRET'))
    const verification = await page.evaluate(async () => {
      const client = await import('/src/api/client.ts')
      const mock = await import('/src/api/mock.ts')
      await client.write('/settings', { method: 'PATCH', body: { timezone: 'Asia/Shanghai', history_days: 30 } })
      let writes = 0
      client.setTransport(async (input, init) => {
        const response = await mock.mockFetch(input, init)
        if (input === '/api/v1/connections' && init?.method === 'POST') { writes++; throw new Error('仅测试响应丢失') }
        return response
      })
      let originalId
      try { await client.write('/connections', { body: { connection: { kind: 'telegram', name: '仅测试原号核查', secrets: { bot_token: 'FAKE_HTTP_TOKEN', chat_id: 'FAKE_HTTP_CHAT' } } } }) }
      catch (error) { originalId = error.details?.operation_id }
      let code
      try { await client.write('/connections', { body: { connection: { name: '禁止重复', secrets: { bot_token: 'FAKE_CHANGED_TOKEN' } } } }) }
      catch (error) { code = error.details?.code }
      const stored = localStorage.getItem(client.OPERATION_STORAGE_KEY) || ''
      await client.lookupOperation(originalId)
      return { writes, originalId, code, leaked: stored.includes('FAKE_HTTP') || stored.includes('FAKE_CHANGED'), pending: client.getPendingIds() }
    })
    assert.equal(verification.writes, 1)
    assert.match(verification.originalId, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    assert.equal(verification.code, 'ACTION_UNCONFIRMED')
    assert.equal(verification.leaked, false)
    assert.deepEqual(verification.pending, [])
    console.log((origin === localOrigin ? '本机 HTTP' : '非本机 IP HTTP') + '：连接保存、配置写入、防重复提交和原号核查通过；仅使用模拟凭据')
    await context.close()
  }
} finally {
  await browser?.close()
  await server.close()
}
