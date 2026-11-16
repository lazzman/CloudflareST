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
const scratch = await mkdtemp(join(tmpdir(), 'cfst-post-rc-fixed-'))
const binary = join(scratch, 'cfst-web')
const password = 'FAKE_ONLY_post_rc_validation_8137'
const outside = [], forbidden = [], writes = [], errors = []
let server, browser
let output = ''
try {
  assert((await readFile(join(root, 'internal/management/web/dist/index.html'), 'utf8')).includes('/assets/'))
  const built = spawnSync('go', ['build', '-o', binary, './cmd/cfst-web'], {cwd: root, encoding: 'utf8', env: {...process.env, GOPROXY: 'off', GOSUMDB: 'off'}})
  assert.equal(built.status, 0, built.stderr)
  const listener = createServer().listen(0, '127.0.0.1')
  await once(listener, 'listening')
  const port = listener.address().port
  await new Promise(resolve => listener.close(resolve))
  const origin = 'http://127.0.0.1:' + port
  server = spawn(binary, ['-listen', '127.0.0.1:' + port, '-data', join(scratch, 'data')], {
    cwd: root, env: {...process.env, CFST_ADMIN_PASSWORD: password, CFST_SECURE_COOKIE: 'false', CFST_TRUSTED_PROXIES: '', CFST_RUNTIME_DIR: join(scratch, 'runtime')}, stdio: ['ignore', 'pipe', 'pipe'],
  })
  server.stdout.on('data', chunk => { output += chunk.toString() })
  server.stderr.on('data', chunk => { output += chunk.toString() })
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { if ((await fetch(origin + '/healthz')).ok) { ready = true; break } } catch {}
    assert.equal(server.exitCode, null, output)
    await delay(100)
  }
  assert(ready, output)
  browser = await chromium.launch({headless: true, args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? {executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH} : {})})
  const context = await browser.newContext({viewport: {width: 1280, height: 900}, serviceWorkers: 'block'})
  await context.route('**/*', route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin !== origin) { outside.push(url.href); return route.abort() }
    if (!['GET', 'HEAD'].includes(request.method()) && url.pathname !== '/api/v1/session') {
      const taskPath = url.pathname === '/api/v1/tasks' || url.pathname.startsWith('/api/v1/tasks/') && url.pathname.split('/').length === 5
      if (!taskPath) { forbidden.push(url.pathname); return route.abort() }
      writes.push({path: url.pathname, method: request.method(), body: request.postDataJSON()})
    }
    return route.continue()
  })
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('dialog', dialog => dialog.accept())
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill(password)
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.goto(origin + '/#/tasks/new')
  await page.getByRole('heading', {name: '新建策略', exact: true}).waitFor()
  await page.getByLabel('策略名称（必填）', {exact: true}).fill('延迟阈值契约复验')
  await page.locator('#latency_max_ms').fill('60001')
  assert.equal(await page.locator('#latency_max_ms').getAttribute('max'), null)
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL(url => url.hash.startsWith('#/tasks/') && url.hash.endsWith('/results'))
  const taskID = new URL(page.url()).hash.split('/')[2]
  const readTask = () => page.evaluate(async taskID => {
    const response = await fetch('/api/v1/tasks/' + taskID)
    if (!response.ok) throw new Error('读取本机任务失败：' + response.status)
    return response.json()
  }, taskID)
  let task = await readTask()
  assert.equal(task.config.latency_max_ms, 60001)
  assert.equal(task.schedule.enabled, false)
  assert.equal(task.dns_auto, false)
  assert.equal(task.notifications.telegram.enabled, false)
  assert.equal(task.notifications.wecom.enabled, false)
  assert.equal(writes.length, 1)
  console.log(JSON.stringify({case: 'POST-RC-02-create', status: 'passed', threshold: task.config.latency_max_ms, realBackendSaved: true, automaticActions: false}))

  await page.goto(origin + '/#/tasks/' + taskID + '/edit')
  await page.getByRole('heading', {name: '编辑策略', exact: true}).waitFor()
  await page.waitForFunction(() => document.getElementById('latency_max_ms')?.value === '60001')
  const beforeInvalid = writes.length
  for (const value of ['0', '-1', '', '1e309']) {
    await page.locator('#latency_max_ms').fill(value)
    await page.getByRole('button', {name: '保存策略', exact: true}).click()
    await page.getByRole('alert').getByText('配置未保存，请修正以下内容', {exact: true}).waitFor()
    assert.equal(await page.locator('#latency_max_ms').getAttribute('aria-invalid'), 'true')
    assert.equal(writes.length, beforeInvalid)
  }
  assert.equal((await readTask()).config.latency_max_ms, 60001)
  console.log(JSON.stringify({case: 'POST-RC-02-invalid', status: 'passed', values: ['0', '-1', '空值', '溢出为无穷大'], businessWrites: 0, persistedThreshold: 60001}))

  await page.locator('#latency_max_ms').fill('1000000000')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL(origin + '/#/tasks/' + taskID + '/results')
  task = await readTask()
  assert.equal(task.config.latency_max_ms, 1000000000)
  assert.equal(writes.length, 2)
  await page.goto(origin + '/#/tasks/' + taskID + '/edit')
  await page.getByRole('heading', {name: '编辑策略', exact: true}).waitFor()
  await page.waitForFunction(() => document.getElementById('latency_max_ms')?.value === '1000000000')
  for (const width of [1280, 320]) {
    await page.setViewportSize({width, height: 900})
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme})
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth))
    }
  }
  assert.deepEqual(outside, [])
  assert.deepEqual(forbidden, [])
  assert.deepEqual(errors, [])
  console.log(JSON.stringify({case: 'POST-RC-02-edit', status: 'passed', threshold: task.config.latency_max_ms, reloaded: true, layouts: [1280, 320], themes: ['light', 'dark']}))
  console.log(JSON.stringify({case: 'isolation', browser: browser.version(), externalRequests: 0, forbiddenBusinessRequests: 0, taskWrites: writes.length, note: '仅临时数据库创建和更新任务；未触发优选、DNS或通知'}))
} catch (error) {
  console.error('定向页面复验失败：', error, '\n服务诊断：', output)
  throw error
} finally {
  await browser?.close()
  if (server && server.exitCode === null) {
    const stopped = once(server, 'exit')
    server.kill('SIGTERM')
    await Promise.race([stopped, delay(5000)])
    if (server.exitCode === null) { server.kill('SIGKILL'); await stopped }
  }
  await rm(scratch, {recursive: true, force: true})
}
