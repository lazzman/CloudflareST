import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createRequire} from 'node:module'
import {once} from 'node:events'
import {mkdir} from 'node:fs/promises'
import {createServer} from 'node:net'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {setTimeout as delay} from 'node:timers/promises'

const root = fileURLToPath(new URL('../', import.meta.url))
const web = join(root, 'internal/management/web')
const output = process.env.UI_SCREENSHOT_DIR || join(web, 'test-results/scaffold')
const require = createRequire(join(web, 'package.json'))
const {chromium} = require('playwright')
const listener = createServer().listen(0, '127.0.0.1')
await once(listener, 'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:' + port
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {cwd: web, stdio: ['ignore', 'pipe', 'pipe']})
let diagnostics = '', browser, page
server.stdout.on('data', value => { diagnostics += value })
server.stderr.on('data', value => { diagnostics += value })
const errors = [], outside = []
try {
  await mkdir(output, {recursive: true})
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}
    assert.equal(server.exitCode, null, diagnostics)
    await delay(100)
  }
  assert(ready, diagnostics)
  browser = await chromium.launch({headless: true, args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? {executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH} : {})})
  const context = await browser.newContext({viewport: {width: 1440, height: 1000}, colorScheme: 'light', reducedMotion: 'reduce', serviceWorkers: 'block'})
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { outside.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('dialog', dialog => dialog.accept())
  const fits = async name => assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + '页面横向溢出')
  const screenshot = async name => { await fits(name); await page.evaluate(() => { if (document.activeElement?.tagName === 'H1') document.activeElement.blur() }); await page.screenshot({path: join(output, name + '.jpg'), type: 'jpeg', quality: 85, fullPage: true}) }
  await page.goto(origin + '/#/login')
  await page.getByRole('heading', {name: '密码登录', exact: true}).waitFor()
  await screenshot('login-desktop')
  await page.setViewportSize({width: 390, height: 844})
  await screenshot('login-mobile')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离外观测试')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.getByRole('link', {name: '边缘节点 · 每日复核', exact: true}).waitFor()
  await screenshot('tasks-mobile')
  await page.getByRole('button', {name: '打开导航', exact: true}).click()
  await page.getByRole('navigation', {name: '主导航', exact: true}).getByRole('link', {name: '服务连接', exact: true}).focus()
  await page.keyboard.press('Escape')
  assert.equal(await page.getByRole('button', {name: '打开导航', exact: true}).getAttribute('aria-expanded'), 'false')
  assert(await page.getByRole('button', {name: '打开导航', exact: true}).evaluate(element => element === document.activeElement))
  await page.setViewportSize({width: 1440, height: 1000})
  await screenshot('tasks-desktop')
  await page.getByRole('button', {name: '收起侧栏', exact: true}).click()
  await fits('折叠侧栏')
  assert(await page.getByRole('navigation', {name: '主导航', exact: true}).getByRole('link', {name: '优选策略', exact: true}).isVisible())
  await page.getByRole('button', {name: '展开侧栏', exact: true}).click()
  await page.getByLabel('外观模式', {exact: true}).selectOption('dark')
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark')
  await page.reload()
  await page.getByRole('heading', {name: '密码登录', exact: true}).waitFor()
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离外观测试')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  assert.equal(await page.locator('html').getAttribute('data-theme'), 'dark')
  await screenshot('tasks-dark')
  await page.getByLabel('外观模式', {exact: true}).selectOption('system')
  await page.emulateMedia({colorScheme: 'dark'})
  await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
  await page.emulateMedia({colorScheme: 'light'})
  await page.waitForFunction(() => document.documentElement.dataset.theme === 'light')
  const routes = [
    ['/tasks', '优选策略', 'tasks', '优选策略'], ['/tasks/new', '新建策略', 'editor', '新建策略'],
    ['/tasks/task-demo', '边缘节点 · 每日复核', 'overview', '任务概览'], ['/tasks/task-demo/runs/run-demo-001', '边缘节点 · 每日复核', 'detail', '执行详情'],
    ['/tasks/task-demo/runs', '运行记录', 'history', '运行记录'], ['/tasks/task-demo/recycle-bin', '已删除运行记录', 'recycle', '回收站'],
    ['/queue', '运行队列', 'queue', '运行队列'], ['/connections', '服务连接', 'connections', '服务连接'],
    ['/operation-logs', '操作日志', 'logs', '操作日志'], ['/settings', '系统设置', 'settings', '系统设置'],
  ]
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({width, height: 1000})
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme})
      for (const [path, heading, name, title] of routes) {
        await page.goto(origin + '/#' + path)
        await page.waitForFunction(title => document.title === title + ' · CloudflareST', title)
        await page.getByRole('heading', {name: heading, exact: true}).waitFor()
        await page.waitForFunction(() => ![...document.querySelectorAll('[role="status"]')].some(element => element.textContent.includes('正在读取服务端状态')))
        await fits(name + '/' + width + '/' + colorScheme)
        if (width === 1440 && colorScheme === 'light' && ['editor', 'detail', 'connections'].includes(name)) await screenshot(name + '-desktop')
        if (width === 320 && name === 'tasks' && colorScheme === 'dark') await screenshot('tasks-320-dark')
      }
    }
  }
  await page.setViewportSize({width: 1440, height: 1000})
  await page.emulateMedia({colorScheme: 'light'})
  await page.goto(origin + '/#/tasks')
  await page.getByRole('link', {name: '边缘节点 · 每日复核', exact: true}).waitFor()
  await page.getByLabel('查找名称／ID', {exact: true}).fill('task-demo')
  assert.equal(await page.getByRole('link', {name: '边缘节点 · 每日复核', exact: true}).count(), 1)
  assert.deepEqual(errors, [])
  assert.deepEqual(outside, [])
  console.log(JSON.stringify({status: 'passed', browser: browser.version(), pageChecks: routes.length * 3 * 2, widths: [1440,390,320], themes: ['light','dark'], themePersistence: true, followsSystem: true, sidebarKeyboard: true, taskIDSearch: true, externalRequests: 0, pageErrors: 0, screenshots: output, note: '独立浏览器与替身API，未发真实DNS或通知'}))
} catch (error) {
  await page?.screenshot({path: join(output, 'failure.jpg'), type: 'jpeg', quality: 85, fullPage: true}).catch(() => {})
  console.error('外观与交互复验失败：', error, diagnostics)
  throw error
} finally {
  await browser?.close()
  if (server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (server.exitCode === null) { server.kill('SIGKILL'); await stopped } }
}
