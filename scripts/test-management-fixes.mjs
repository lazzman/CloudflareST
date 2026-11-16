import assert from 'node:assert/strict'
import {spawn, spawnSync} from 'node:child_process'
import {createRequire} from 'node:module'
import {once} from 'node:events'
import {mkdtemp, readFile, rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createServer} from 'node:net'
import {fileURLToPath} from 'node:url'
import {setTimeout as delay} from 'node:timers/promises'

const root = fileURLToPath(new URL('../', import.meta.url))
const require = createRequire(join(root, 'internal/management/web/package.json'))
const {chromium} = require('playwright')
const scratch = await mkdtemp(join(tmpdir(), 'cfst-fixed-boundaries-'))
const binary = join(scratch, 'cfst-web')
const password = 'FAKE_local_regression_only_2026'
const checks = []
let server
let browser
let probe
let diagnostics = ''

async function freePort() {
  const listener = createServer().listen(0, '127.0.0.1')
  await once(listener, 'listening')
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  return port
}

try {
  const index = await readFile(join(root, 'internal/management/web/dist/index.html'), 'utf8')
  assert(index.includes('/assets/'), '先构建生产前端资源')
  const built = spawnSync('go', ['build', '-o', binary, './cmd/cfst-web'], {cwd: root, encoding: 'utf8', env: {...process.env, GOPROXY: 'off', GOSUMDB: 'off'}})
  assert.equal(built.status, 0, built.stderr)
  const origin = 'http://127.0.0.1:' + await freePort()
  server = spawn(binary, ['-listen', new URL(origin).host, '-data', join(scratch, 'data')], {cwd: root, env: {...process.env, CFST_ADMIN_PASSWORD: password, CFST_SECURE_COOKIE: 'false', CFST_RUNTIME_DIR: join(scratch, 'runtime'), CFST_TRUSTED_PROXIES: ''}, stdio: ['ignore', 'pipe', 'pipe']})
  server.stdout.on('data', chunk => { diagnostics += chunk.toString() })
  server.stderr.on('data', chunk => { diagnostics += chunk.toString() })
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { if ((await fetch(origin + '/healthz')).ok) { ready = true; break } } catch {}
    assert.equal(server.exitCode, null, diagnostics)
    await delay(100)
  }
  assert(ready, diagnostics)
  browser = await chromium.launch({headless: true, args: ['--disable-background-networking'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? {executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH} : {})})
  const context = await browser.newContext({viewport: {width: 1280, height: 900}, serviceWorkers: 'block'})
  const outsideRequests = []
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { outsideRequests.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  const pageErrors = []
  page.on('pageerror', error => pageErrors.push(error.message))
  page.on('dialog', dialog => dialog.accept())
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill(password)
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.waitForURL('**/#/tasks')

  async function fillTask(name) {
    await page.goto(origin + '/#/tasks/new')
    await page.getByLabel('策略名称（必填）', {exact: true}).fill(name)
    await page.locator('.source-row select').first().selectOption('ip')
    await page.getByLabel('来源 1 值', {exact: true}).fill('127.0.0.1')
    await page.getByLabel(/探测方式/).selectOption('tcp')
    await page.getByRole('checkbox', {name: '启用下载测速与过滤', exact: true}).uncheck()
  }
  const submitted = []
  let dropResponse = true
  await page.route('**/api/v1/tasks', async route => {
    if (route.request().method() === 'POST') {
      submitted.push(route.request().postDataJSON())
      if (dropResponse) {
        dropResponse = false
        const accepted = await route.fetch()
        assert.equal(accepted.status(), 202)
        return route.abort('connectionreset')
      }
    }
    return route.continue()
  })
  await fillTask('未决创建操作恢复复验')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.getByRole('alert').filter({hasText: /未能确认|未确认/}).first().waitFor()
  const originalId = submitted[0].operation_id
  const before = await page.evaluate(async () => (await (await fetch('/api/v1/tasks')).json()).length)
  assert.equal(before, 1)
  await page.reload()
  await fillTask('未决创建操作恢复复验')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.getByRole('alert').filter({hasText: /原操作|只读|未确认/}).first().waitFor()
  const after = await page.evaluate(async () => (await (await fetch('/api/v1/tasks')).json()).length)
  assert.equal(after, 1, '刷新后的同一未决创建操作不能换号重建')
  assert.equal(submitted.length, 1, '第二次点击不得发送新的创建请求')
  checks.push({case: 'F-03', status: 'passed', tasksBefore: before, tasksAfterReloadClick: after, posts: submitted.length})

  async function authenticatedWrite(path, method, body) {
    return page.evaluate(async ({path, method, body}) => {
      const session = await (await fetch('/api/v1/session')).json()
      const response = await fetch(path, {method, headers: {'Content-Type': 'application/json', 'X-CSRF-Token': session.csrf_token}, body: JSON.stringify({...body, operation_id: crypto.randomUUID()})})
      const result = await response.json()
      if (!response.ok) throw new Error(JSON.stringify(result))
      return result
    }, {path, method, body})
  }
  await page.goto(origin + '/#/settings')
  await page.getByLabel('统一系统时区').fill('UTC')
  await page.getByRole('button', {name: '保存系统设置', exact: true}).click()
  await page.getByRole('status').filter({hasText: '已收到操作收据'}).waitFor()
  const timezone = await page.evaluate(async () => (await (await fetch('/api/v1/settings')).json()).timezone)
  assert.equal(timezone, 'UTC', '未决创建操作不应阻断无关设置保存')
  await page.getByRole('button', {name: /操作核查/}).click()
  const tracker = page.getByRole('dialog', {name: '原操作号只读核查'})
  await tracker.locator('li').filter({hasText: originalId}).getByRole('button', {name: '只读查询', exact: true}).click()
  await tracker.getByText('关联对象：', {exact: false}).waitFor()
  assert((await tracker.innerText()).includes(originalId))
  assert.equal(submitted.length, 1, '查询收据不应产生创建动作')
  await page.keyboard.press('Escape')
  checks.push({case: 'C01-unrelated-action-and-readback', status: 'passed'})

  const defaults = await page.evaluate(async () => (await (await fetch('/api/v1/defaults')).json()).config)
  const task = {name: 'UTC计划显示复验', config: {...defaults, sources: [{kind: 'ip', value: '127.0.0.1'}], probe_mode: 'tcp', download_enabled: false}, schedule: {enabled: true, kind: 'cron', expression: '0 0 1 1 *'}, dns_auto: false, targets: [], notifications: {telegram: {enabled: false, connection_id: ''}, wecom: {enabled: false, connection_id: ''}}}
  const receipt = await authenticatedWrite('/api/v1/tasks', 'POST', {task})
  const saved = await page.evaluate(async id => (await (await fetch('/api/v1/tasks/' + id)).json()), receipt.object_id)
  await page.goto(origin + '/#/tasks/' + saved.id)
  await page.getByRole('heading', {name: task.name, exact: true}).waitFor()
  await page.waitForFunction(() => document.querySelector('.global-status')?.textContent?.includes('UTC'))
  const expected = new Intl.DateTimeFormat('zh-CN', {timeZone: 'UTC', dateStyle: 'short', timeStyle: 'medium', hour12: false}).format(new Date(saved.schedule.next_run_at))
  const nextTime = page.locator('dl > div').filter({has: page.locator('dt', {hasText: '下次未来时点'})}).locator('dd')
  await page.waitForFunction(value => Array.from(document.querySelectorAll('dd')).some(item => item.textContent === value), expected)
  assert.equal(await nextTime.innerText(), expected)
  checks.push({case: 'F-04', status: 'passed', timezone: 'UTC', serverInstant: saved.schedule.next_run_at, displayed: expected})

  await fillTask('每CIDR独立数量复验')
  await page.locator('.editor-advanced > summary').click()
  await page.locator('.source-row select').first().selectOption('cidr')
  await page.getByLabel('来源 1 值', {exact: true}).fill('192.0.2.0/28')
  await page.locator('.source-row').first().locator('input[type=number]').fill('5')
  await page.getByRole('button', {name: '添加来源', exact: true}).click()
  await page.locator('.source-row').nth(1).locator('select').first().selectOption('cidr')
  await page.getByLabel('来源 2 值', {exact: true}).fill('198.51.100.0/28')
  await page.locator('.source-row').nth(1).locator('input[type=number]').fill('8')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL(url => url.hash.startsWith('#/tasks/') && !url.hash.endsWith('/new'))
  const cidrId = new URL(page.url()).hash.split('/')[2]
  const cidrTask = await page.evaluate(async id => (await (await fetch('/api/v1/tasks/' + id)).json()), cidrId)
  assert.deepEqual(cidrTask.config.sources.map(source => source.count), [5, 8])
  await page.goto(origin + '/#/tasks/' + cidrId + '/edit')
  await page.locator('.editor-advanced > summary').click()
  assert.equal(await page.locator('.source-row').first().locator('input[type=number]').inputValue(), '5')
  assert.equal(await page.locator('.source-row').nth(1).locator('input[type=number]').inputValue(), '8')
  await page.locator('.source-row').first().locator('input[type=number]').fill('0')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.getByRole('heading', {name: '配置未保存，请修正以下内容', exact: true}).waitFor()
  await page.locator('.source-row').first().locator('input[type=number]').fill('')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL('**/#/tasks/' + cidrId + '/results')
  const restored = await page.evaluate(async id => (await (await fetch('/api/v1/tasks/' + id)).json()), cidrId)
  assert.equal(restored.config.sources[0].count || 0, 0)
  assert.equal(restored.config.sources[1].count, 8)
  checks.push({case: 'F-08', status: 'passed', overrides: [5, 8], zeroRejected: true, blankUsesDefault: true})

  probe = createServer(socket => socket.end()).listen(0, '127.0.0.1')
  await once(probe, 'listening')
  const localTask = {...task, name: '修复后真实单IP回环', config: {...task.config, ipv4_limit: 1, tcp_port: probe.address().port, probe_count: 1}, schedule: {enabled: false, kind: 'interval', interval_minutes: 60}}
  const created = await authenticatedWrite('/api/v1/tasks', 'POST', {task: localTask})
  const queued = await authenticatedWrite('/api/v1/tasks/' + created.object_id + '/runs', 'POST', {})
  let result
  for (let attempt = 0; attempt < 100; attempt++) {
    result = await page.evaluate(async path => (await (await fetch(path)).json()), '/api/v1/tasks/' + created.object_id + '/runs/' + queued.object_id)
    if (!['queued', 'running'].includes(result.status)) break
    await delay(100)
  }
  assert.equal(result.status, 'completed')
  assert.equal(result.result.candidates.length, 1)
  assert.equal(result.result.candidates[0].ip, '127.0.0.1')
  assert.equal(result.result.candidates[0].rank, 1)
  assert.equal(result.deliveries.length, 0)
  checks.push({case: 'loopback-regression', status: result.status, candidates: 1, deliveries: 0})
  const runPath = '/api/v1/tasks/' + created.object_id + '/runs/' + queued.object_id
  const paged = page.waitForResponse(response => response.url().includes(runPath + '/candidates?') && response.status() === 200)
  await page.goto(origin + '/#/tasks/' + created.object_id + '/runs/' + queued.object_id)
  const candidateResponse = await (await paged).json()
  assert.equal(candidateResponse.total, 1)
  const candidateRow = page.locator('tbody tr').filter({has: page.locator('code', {hasText: '127.0.0.1'})})
  await candidateRow.waitFor()
  assert((await candidateRow.innerText()).includes('下载未测试'))
  await candidateRow.getByText('全部命中来源（1）', {exact:true}).click()
  assert((await candidateRow.innerText()).includes('source-001'))
  const metadata = await page.evaluate(async path => (await (await fetch(path)).json()), runPath + '?view=summary')
  const progress = await page.evaluate(async path => (await (await fetch(path)).json()), runPath + '/progress')
  assert.equal(metadata.result.candidates, null)
  assert.equal(metadata.candidate_summary.total, 1)
  assert.equal(Object.hasOwn(progress, 'result'), false)
  assert(progress.stage_progress.length > 0)
  checks.push({case:'real-paged-detail', status:'passed', candidates:1, summaryHasCandidates:false, progressHasCandidates:false, sourceVisible:true, untestedDownloadExplicit:true})
  assert.deepEqual(outsideRequests, [])
  assert.deepEqual(pageErrors, [])
  console.log(checks.map(item => JSON.stringify(item)).join('\n'))
} catch (error) {
  console.error(diagnostics)
  throw error
} finally {
  await browser?.close()
  if (probe) await new Promise(resolve => probe.close(resolve))
  if (server && server.exitCode === null) {
    const closed = once(server, 'exit'); server.kill('SIGTERM')
    await Promise.race([closed, delay(5000)])
    if (server.exitCode === null) { server.kill('SIGKILL'); await closed }
  }
  await rm(scratch, {recursive: true, force: true})
}
