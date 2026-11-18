import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdir } from 'node:fs/promises'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const workdir = fileURLToPath(new URL('../', import.meta.url))
const origin = 'http://127.0.0.1:5191'
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', '5191', '--strictPort'], { cwd: workdir, stdio: ['ignore', 'pipe', 'pipe'] })
let serverOutput = ''
server.stdout.on('data', chunk => { serverOutput += chunk.toString() })
server.stderr.on('data', chunk => { serverOutput += chunk.toString() })
let browser
let page
const errors = []
const blocked = []
const checks = []
try {
  let ready = false
  for (let attempt = 0; attempt < 60; attempt++) {
    try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}
    await delay(150)
  }
  assert(ready, '本地 Vite 未启动：' + serverOutput)
  browser = await chromium.launch({ headless: true, args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) })
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, timezoneId: 'America/Los_Angeles', reducedMotion: 'reduce', serviceWorkers: 'block' })
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { blocked.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', entry => { if (entry.type() === 'error') errors.push(entry.text()) })
  page.on('dialog', dialog => dialog.accept())
  async function login() {
    await page.getByLabel('管理密码').fill('仅本地预览测试')
    await page.getByRole('button', { name: '登录管理端' }).click()
    await page.locator('.topbar').waitFor()
  }
  async function open(path, heading) {
    await page.goto(origin + '/#' + path)
    await page.getByRole('heading', { name: heading, exact: true }).waitFor()
  }
  async function installTransport(receipt) {
    await page.evaluate(async receipt => {
      const client = await import('/src/api/client.ts')
      const mock = await import('/src/api/mock.ts')
      window.__fixRequests = []
      const instant = '2027-01-01T00:00:00Z'
      function fixedTimes(value) {
        if (!value || typeof value !== 'object') return
        for (const [key, child] of Object.entries(value)) {
          if (typeof child === 'string' && (key.endsWith('_at') || key === 'at')) value[key] = instant
          else fixedTimes(child)
        }
      }
      client.setTransport(async (input, init = {}) => {
        const path = String(input)
        const method = init.method || 'GET'
        window.__fixRequests.push({ path, method, body: init.body ? JSON.parse(String(init.body)) : undefined })
        if (receipt && path === '/api/v1/operations/' + receipt.operation_id) return new Response(JSON.stringify(receipt), { headers: { 'Content-Type': 'application/json' } })
        const response = await mock.mockFetch(input, init)
        if (method === 'POST' && path === '/api/v1/tasks' && window.__loseNextCreate) {
          window.__loseNextCreate = false
          window.__lostReceipt = await response.json()
          throw new TypeError('模拟已保存但响应丢失')
        }
        if (method !== 'GET' || !response.ok) return response
        const value = await response.json()
        fixedTimes(value)
        if (path === '/api/v1/tasks/' + window.__zeroDefaultId) value.config.sources[2].count = 0
        return new Response(JSON.stringify(value), { status: response.status, headers: { 'Content-Type': 'application/json' } })
      })
    }, receipt)
  }
  const requests = () => page.evaluate(() => window.__fixRequests)
  const writes = async () => (await requests()).filter(item => item.method !== 'GET')
  async function waitForRequest(predicate) {
    for (let attempt = 0; attempt < 50; attempt++) {
      const found = (await requests()).findLast(predicate)
      if (found) return found
      await delay(50)
    }
    assert.fail('未观察到预期请求')
  }
  await page.goto(origin + '/#/login')
  await login()
  await installTransport()

  await open('/tasks/new', '新建策略')
  await page.getByLabel('策略名称（必填）').fill('CIDR 数量回归')
  await page.getByLabel('来源 1 类型').selectOption('cidr')
  await page.getByLabel('来源 1 值', { exact: true }).fill('192.0.2.0/24')
  for (const [index, address] of [[2, '198.51.100.0/24'], [3, '203.0.113.0/24']]) {
    await page.getByRole('button', { name: '添加来源', exact: true }).click()
    await page.getByLabel('来源 ' + index + ' 类型').selectOption('cidr')
    await page.getByLabel('来源 ' + index + ' 值', { exact: true }).fill(address)
  }
  const firstCount = page.getByLabel('来源 1 初采数量', { exact: true })
  assert.equal(await firstCount.getAttribute('min'), '1')
  assert.equal(await firstCount.getAttribute('max'), '100000')
  assert.equal(await firstCount.getAttribute('step'), '1')
  for (const invalid of ['0', '100001', '1.5']) {
    const before = (await writes()).length
    await firstCount.fill(invalid)
    await page.getByRole('button', { name: '保存策略' }).click()
    await page.getByRole('alert').getByText('配置未保存，请修正以下内容').waitFor()
    assert.equal((await writes()).length, before, '无效数量不能提交')
    await page.getByRole('link', { name: '来源 1 初采数量须为 1～100000 的整数，或留空使用默认值' }).click()
    assert(await firstCount.evaluate(element => element === document.activeElement))
  }
  await firstCount.fill('32')
  await page.getByLabel('来源 2 初采数量', { exact: true }).fill('100000')
  await page.getByLabel('来源 3 初采数量', { exact: true }).fill('64')
  await page.getByLabel('来源 3 初采数量', { exact: true }).fill('')
  for (const width of [1280, 320]) {
    await page.setViewportSize({ width, height: 900 })
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({ colorScheme })
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'CIDR 表单不应全页溢出')
      assert(await page.locator('.source-row').evaluateAll(rows => rows.every(row => row.scrollWidth <= row.clientWidth)), 'CIDR 行不应横向溢出')
    }
  }
  await page.getByRole('button', { name: '保存策略' }).click()
  await page.getByRole('heading', { name: 'CIDR 数量回归', exact: true }).waitFor()
  const saved = (await writes()).findLast(item => item.path === '/api/v1/tasks')
  assert(saved)
  assert.deepEqual(saved.body.task.config.sources.map(source => source.count), [32, 100000, undefined])
  const taskId = new URL(page.url()).hash.split('/')[2]
  await page.evaluate(taskId => { window.__zeroDefaultId = taskId }, taskId)
  await open('/tasks/' + taskId + '/edit', '编辑策略')
  assert.equal(await page.getByLabel('来源 1 初采数量', { exact: true }).inputValue(), '32')
  assert.equal(await page.getByLabel('来源 2 初采数量', { exact: true }).inputValue(), '100000')
  assert.equal(await page.getByLabel('来源 3 初采数量', { exact: true }).inputValue(), '', '后端 count=0 应回读为默认空值')
  await page.getByLabel('来源 1 初采数量', { exact: true }).fill('1')
  await page.getByLabel('来源 2 初采数量', { exact: true }).fill('')
  await page.getByRole('button', { name: '保存策略' }).click()
  await page.getByRole('heading', { name: 'CIDR 数量回归', exact: true }).waitFor()
  const updated = (await writes()).findLast(item => item.method === 'PATCH' && item.path === '/api/v1/tasks/' + taskId)
  assert.deepEqual(updated.body.task.config.sources.map(source => source.count), [1, undefined, undefined])
  checks.push('F-08：独立数量、边界校验/焦点、空值默认、保存/回读、320px 与双主题')
  await page.setViewportSize({ width: 1280, height: 900 })

  assert.equal(await page.evaluate(() => Intl.DateTimeFormat().resolvedOptions().timeZone), 'America/Los_Angeles')
  for (const [timezone, expected, localInput] of [['UTC', '2027/1/1 00:00:00', '2027-01-01T00:00'], ['Asia/Shanghai', '2027/1/1 08:00:00', '2027-01-01T08:00']]) {
    await open('/settings', '系统设置')
    await page.getByLabel('统一系统时区').fill(timezone)
    await page.getByRole('button', { name: '保存系统设置' }).click()
    await page.locator('.global-status').getByText(timezone, { exact: true }).waitFor()
    await open('/tasks/task-demo', '边缘节点 · 每日复核')
    assert.equal(await page.locator('dt').filter({ hasText: '下次未来时点' }).locator('..').locator('dd').innerText(), expected)
    assert((await page.locator('.target-entry').innerText()).includes('已确认写入：' + expected))
    await open('/tasks', '优选策略')
    assert((await page.getByRole('table').innerText()).includes(expected))
    await open('/queue', '运行队列')
    await page.getByText('入队：' + expected, { exact: false }).waitFor()
    await open('/tasks/task-demo/runs/run-demo-001', '边缘节点 · 每日复核')
    assert((await page.locator('main').innerText()).includes(expected))
    for (const [path, heading, startLabel, button, apiPath] of [
      ['/tasks/task-demo/runs', '运行记录', '入队开始时间', '应用筛选', '/api/v1/tasks/task-demo/runs'],
      ['/operation-logs', '操作日志', '开始时间', '查询日志', '/api/v1/operation-logs'],
    ]) {
      await open(path, heading)
      assert((await page.getByRole('table').innerText()).includes(expected))
      assert((await page.locator('#filter-timezone').innerText()).includes(timezone))
      await page.evaluate(() => { window.__fixRequests = [] })
      await page.getByLabel(startLabel, { exact: true }).fill(localInput)
      await page.getByRole('button', { name: button, exact: true }).click()
      const request = await waitForRequest(item => item.path.startsWith(apiPath + '?') && new URL(item.path, origin).searchParams.get('start') === '2027-01-01T00:00:00.000Z')
      assert.equal(new URL(request.path, origin).searchParams.get('start'), '2027-01-01T00:00:00.000Z')
    }
  }
  checks.push('F-04：洛杉矶浏览器中 UTC/上海显示一致，计划/运行/日志/队列/目标及筛选同口径')

  await open('/tasks/new', '新建策略')
  await page.getByLabel('策略名称（必填）').fill('C01 响应丢失回归')
  await page.evaluate(() => { window.__loseNextCreate = true })
  await page.getByRole('button', { name: '保存策略' }).click()
  await page.getByRole('alert').getByText('NETWORK_ERROR', { exact: true }).waitFor()
  const receipt = await page.evaluate(() => window.__lostReceipt)
  assert(receipt.operation_id)
  const storage = await page.evaluate(() => localStorage.getItem('cloudflarest.pending-operation-ids'))
  assert.equal(JSON.parse(storage).operations[0].operation_id, receipt.operation_id)
  assert.match(JSON.parse(storage).operations[0].fingerprint, /^[0-9a-f]{64}$/)
  assert(!storage.includes('C01 响应丢失回归'))
  await page.reload()
  await login()
  await installTransport(receipt)
  await open('/tasks/new', '新建策略')
  await page.getByLabel('策略名称（必填）').fill('C01 响应丢失回归')
  await page.getByRole('button', { name: '保存策略' }).click()
  await page.getByRole('alert').getByText('ACTION_UNCONFIRMED', { exact: true }).waitFor()
  assert((await page.getByRole('alert').innerText()).includes(receipt.operation_id))
  assert.equal((await writes()).length, 0, '刷新重登后同动作不能换号')
  await page.setViewportSize({ width: 320, height: 900 })
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), '待核查提示不应造成手机横向溢出')
  await page.setViewportSize({ width: 1280, height: 900 })
  await open('/settings', '系统设置')
  await page.getByLabel('统一系统时区').fill('UTC')
  await page.getByRole('button', { name: '保存系统设置' }).click()
  await page.locator('.global-status').getByText('UTC', { exact: true }).waitFor()
  assert.equal((await writes()).length, 1, '无关设置操作仍可提交')
  await page.getByRole('button', { name: '操作核查（1）', exact: true }).click()
  const dialog = page.getByRole('dialog')
  assert((await dialog.innerText()).includes(receipt.operation_id))
  await dialog.getByRole('button', { name: '只读查询', exact: true }).click()
  await page.getByRole('button', { name: '操作核查', exact: true }).waitFor()
  assert.equal((await writes()).length, 1, '查询原号不能自动重交')
  assert((await requests()).some(item => item.path === '/api/v1/operations/' + receipt.operation_id && item.method === 'GET'))
  await dialog.getByRole('button', { name: '关闭', exact: true }).click()
  checks.push('F-03：丢响应→刷新→重登保留原号与锁，拒绝新号/允许无关操作，仅 GET 核查')

  await page.evaluate(() => {
    localStorage.setItem('cloudflarest.pending-operation-ids', JSON.stringify([crypto.randomUUID()]))
  })
  await page.reload()
  await login()
  await installTransport()
  await open('/tasks/new', '新建策略')
  await page.getByLabel('策略名称（必填）').fill('旧格式不能静默另建')
  await page.getByRole('button', { name: '保存策略' }).click()
  await page.getByRole('alert').getByText('ACTION_UNCONFIRMED', { exact: true }).waitFor()
  assert.equal((await writes()).length, 0)
  await page.getByRole('button', { name: '操作核查（1）', exact: true }).click()
  await page.getByRole('dialog').getByText('旧版记录缺少动作范围。完成原号核查前暂不允许新建任务或连接，其他对象操作仍可继续。', { exact: true }).waitFor()
  checks.push('F-03：旧格式 pending ID 明确提示且保守阻止新建')
  assert.deepEqual(blocked, [], '不得访问外部服务')
  assert.deepEqual(errors, [], '浏览器不应有运行时异常')
  console.log('Web 修复浏览器回归通过：\n- ' + checks.join('\n- '))
} catch (error) {
  if (page) {
    await mkdir(new URL('../test-results/', import.meta.url), { recursive: true })
    await page.screenshot({ path: fileURLToPath(new URL('../test-results/fixes-failure.png', import.meta.url)), fullPage: true })
    console.error('失败页面：' + page.url() + '\n' + (await page.locator('[role=alert]').allTextContents()).join('\n'))
  }
  throw error
} finally {
  await browser?.close()
  server.kill('SIGTERM')
}
