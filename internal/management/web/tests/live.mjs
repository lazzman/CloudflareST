import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const root = fileURLToPath(new URL('../../../../', import.meta.url))
const scratch = await mkdtemp(join(tmpdir(), 'cfst-r3-live-'))
const binary = join(scratch, 'cfst-r3-live')
const password = 'CFST-R3-仅本地冒烟-非真实管理密码-20261115'
const blocked = []
const errors = []
const checks = []
const httpFailures = []
let logoutStarted = false
let server
let browser
let page
let serverOutput = ''
async function run(command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, stdio: ['ignore', 'pipe', 'pipe'], ...options })
  let output = ''
  child.stdout.on('data', chunk => { output += chunk })
  child.stderr.on('data', chunk => { output += chunk })
  const [code] = await once(child, 'exit')
  assert.equal(code, 0, command + ' 失败：' + output)
  return output
}
try {
  const index = await readFile(new URL('../dist/index.html', import.meta.url), 'utf8')
  assert(index.includes('/assets/'), '请先执行 npm run build')
  await run('go', ['build', '-o', binary, './cmd/cfst-web'], { env: { ...process.env, GOPROXY: 'off', GOSUMDB: 'off' } })
  checks.push('Go构建成功，嵌入本轮生产前端资源')
  const listener = createServer()
  listener.listen(0, '127.0.0.1')
  await once(listener, 'listening')
  const port = listener.address().port
  await new Promise((resolve, reject) => listener.close(error => error ? reject(error) : resolve()))
  const origin = 'http://127.0.0.1:' + port
  server = spawn(binary, ['-listen', '127.0.0.1:' + port, '-data', join(scratch, 'data')], {
    cwd: root, stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, CFST_ADMIN_PASSWORD: password, CFST_RUNTIME_DIR: join(scratch, 'runtime'), CFST_TRUSTED_PROXIES: '', CFST_SECURE_COOKIE: 'false' },
  })
  server.stdout.on('data', chunk => { serverOutput += chunk })
  server.stderr.on('data', chunk => { serverOutput += chunk })
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { const response = await fetch(origin, { redirect: 'error' }); if (response.ok) { ready = true; break } } catch {}
    assert(server.exitCode === null, '服务提前退出：' + serverOutput)
    await delay(100)
  }
  assert(ready, '临时服务未启动：' + serverOutput)
  browser = await chromium.launch({ headless: true, args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH } : {}) })
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, serviceWorkers: 'block', reducedMotion: 'reduce' })
  const requests = []
  const writes = []
  await context.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== origin) { blocked.push(url.href); return route.abort() }
    requests.push(url.pathname)
    if (request.method() !== 'GET') writes.push({ method: request.method(), path: url.pathname, body: request.postDataJSON() })
    assert(!url.pathname.match(/\/tasks\/[^/]+\/runs$/) || request.method() === 'GET', '冒烟禁止发起运行')
    assert(!url.pathname.match(/\/(dns-deliveries|notifications)$/), '冒烟禁止DNS与消息投递')
    return route.continue()
  })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', entry => {
    if (entry.type() === 'error' && !(logoutStarted && entry.text().includes('401 (Unauthorized)'))) errors.push(entry.text())
  })
  page.on('response', response => {
    if (response.status() >= 400) httpFailures.push({ path: new URL(response.url()).pathname, status: response.status(), logoutStarted })
  })
  await page.goto(origin + '/#/login')
  assert.equal(await page.locator('.mock-banner').count(), 0)
  await page.getByLabel('管理密码', { exact: true }).fill(password)
  await page.getByRole('button', { name: '登录管理端', exact: true }).click()
  await page.waitForURL('**/#/tasks')
  await page.getByRole('heading', { name: '优选策略', exact: true }).waitFor()
  checks.push('真实HTTP登录与嵌入资源加载（无mock适配器）')
  await page.getByRole('link', { name: '新建策略', exact: true }).click()
  await page.getByLabel('策略名称（必填）', { exact: true }).fill('R3真实HTTP本地保存冒烟')
  await page.locator('.source-row select').first().selectOption('ip')
  await page.getByLabel('来源 1 值', { exact: true }).fill('127.0.0.1')
  await page.locator('#editor-step-measure').click()
  await page.getByLabel(/探测方式/).selectOption('tcp')
  await page.getByRole('checkbox', { name: '启用下载测速与过滤', exact: true }).uncheck()
  await page.getByRole('button', { name: '保存策略', exact: true }).click()
  await page.waitForURL(/#\/tasks\/[^/]+\/results$/)
  await page.getByRole('heading', { name: 'R3真实HTTP本地保存冒烟', exact: true }).waitFor()
  const taskId = new URL(page.url()).hash.split('/')[2]
  const saved = await page.evaluate(async taskId => {
    const response = await fetch('/api/v1/tasks/' + taskId, { credentials: 'same-origin' })
    if (!response.ok) throw new Error('任务读取失败')
    return response.json()
  }, taskId)
  assert.deepEqual(saved.config.sources.map(source => ({ kind: source.kind, value: source.value })), [{ kind: 'ip', value: '127.0.0.1' }])
  assert.equal(saved.config.probe_mode, 'tcp')
  assert.equal(saved.config.download_enabled, false)
  assert.deepEqual(saved.config.filter_order, ['latency', 'loss'])
  assert.equal(saved.schedule.enabled, false)
  assert.equal(saved.dns_auto, false)
  assert.equal(saved.notifications.telegram.enabled, false)
  assert.equal(saved.notifications.wecom.enabled, false)
  checks.push('真实表单保存及API回读：仅127.0.0.1、TCP、下载/计划/DNS/通知均关闭；未触发运行')
  await page.goto(origin + '/#/queue')
  await page.getByRole('heading', { name: '运行队列', exact: true }).waitFor()
  await page.getByText('当前席位空闲。', { exact: true }).waitFor()
  await page.goto(origin + '/#/operation-logs')
  await page.getByRole('heading', { name: '操作日志', exact: true }).waitFor()
  await page.getByLabel('对象 / 原尝试 ID', { exact: true }).fill(taskId)
  await page.getByLabel('结果', { exact: true }).selectOption('completed')
  await page.getByRole('button', { name: '查询日志', exact: true }).click()
  await page.getByRole('table').waitFor()
  assert((await page.getByRole('table').innerText()).includes(taskId))
  await page.getByLabel('结果', { exact: true }).selectOption('failed')
  await page.getByRole('button', { name: '查询日志', exact: true }).click()
  await page.getByText('当前条件没有日志。日志没有回收站，不支持逐条删除。', { exact: true }).waitFor()
  checks.push('真实空队列、日志对象+结果AND筛选')
  await page.setViewportSize({ width: 320, height: 900 })
  await page.getByRole('button', { name: '打开导航', exact: true }).click()
  assert.deepEqual(errors, [], '退出前不应有浏览器错误')
  logoutStarted = true
  await page.getByRole('button', { name: '退出登录', exact: true }).click()
  await page.waitForURL('**/#/login')
  const authenticated = await page.evaluate(async () => (await (await fetch('/api/v1/session')).json()).authenticated)
  assert.equal(authenticated, false)
  assert.equal(blocked.length, 0, '任何页面公网请求都应阻断')
  assert.deepEqual(errors, [], '浏览器不应出现运行时错误')
  assert(httpFailures.every(failure => failure.logoutStarted && failure.status === 401 && failure.path.startsWith('/api/v1/') && failure.path !== '/api/v1/session'), '只有明确退出后的受保护读取可返回401：' + JSON.stringify(httpFailures))
  assert(requests.some(path => path.startsWith('/assets/')), '必须加载Go嵌入生产资源')
  assert(!requests.some(path => path.startsWith('/src/') || path.includes('@vite')), '不得依赖开发服务器')
  assert(writes.every(write => ['/api/v1/session', '/api/v1/tasks'].includes(write.path)), '仅允许登录、配置保存和退出')
  checks.push('320px退出并核验真实会话失效；退出后的受保护读取401为预期拒绝；无运行/DNS/消息请求，外网请求为0')
  console.log('真实Go同源冒烟通过：\n- ' + checks.join('\n- '))
} catch (error) {
  if (page) {
    await mkdir(new URL('../test-results/', import.meta.url), { recursive: true })
    await page.screenshot({ path: fileURLToPath(new URL('../test-results/live-failure.png', import.meta.url)), fullPage: true })
    console.error('失败页面：' + page.url() + '\n' + (await page.locator('[role=alert]').allTextContents()).join('\n'))
  }
  console.error(serverOutput)
  throw error
} finally {
  await browser?.close()
  if (server && server.exitCode === null) {
    const stopped = once(server, 'exit')
    server.kill('SIGTERM')
    await Promise.race([stopped, delay(5000)])
    if (server.exitCode === null) { server.kill('SIGKILL'); await stopped }
  }
  await rm(scratch, { recursive: true, force: true })
}
