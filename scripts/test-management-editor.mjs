import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createRequire} from 'node:module'
import {once} from 'node:events'
import {mkdir} from 'node:fs/promises'
import {createServer} from 'node:net'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {setTimeout as delay} from 'node:timers/promises'

const root = fileURLToPath(new URL('../', import.meta.url)), web = join(root, 'internal/management/web')
const {chromium} = createRequire(join(web, 'package.json'))('playwright')
const output = process.env.EDITOR_OUTPUT_DIR || join(web, 'test-results/editor')
const listener = createServer().listen(0, '127.0.0.1')
await once(listener, 'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:' + port
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {cwd: web, stdio: ['ignore', 'pipe', 'pipe']})
let diagnostics = '', browser, page
const errors = [], outside = [], layouts = []
server.stdout.on('data', value => diagnostics += value)
server.stderr.on('data', value => diagnostics += value)
try {
  await mkdir(output, {recursive: true})
  let ready = false
  for (let attempt = 0; attempt < 100; attempt++) {
    try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}
    assert.equal(server.exitCode, null, diagnostics)
    await delay(100)
  }
  assert(ready, diagnostics)
  browser = await chromium.launch({headless: true, ignoreDefaultArgs: ['--hide-scrollbars'], args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? {executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH} : {})})
  const context = await browser.newContext({viewport: {width: 1440, height: 900}, colorScheme: 'light', reducedMotion: 'reduce', serviceWorkers: 'block'})
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { outside.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  page.on('dialog', dialog => dialog.accept())
  const idle = () => page.waitForFunction(() => !document.querySelector('[data-resource-loading]'))
  const open = async path => { await page.goto(origin + '/#' + path); await page.locator('.strategy-editor').waitFor(); await idle() }
  const choose = async (section, advanced) => {
    await page.locator('#editor-step-' + section).click()
    if (advanced) await page.locator('#advanced-tab-' + advanced).click()
  }
  const resetScroll = () => page.evaluate(async () => { const {scrollOwner} = await import('/src/lib/scroll.ts'); scrollOwner().scrollTo({top: 0, behavior: 'instant'}); document.activeElement?.blur() })
  const footerVisible = () => page.locator('.strategy-editor .form-footer').evaluate(element => {
    const box = element.getBoundingClientRect(), main = document.getElementById('main-content').getBoundingClientRect()
    return box.top >= Math.max(0, main.top) && box.bottom <= Math.min(innerHeight, main.bottom) + 1
  })
  const state = () => page.evaluate(() => ({writes: window.__editorWrites, task: window.__savedEditorTask}))
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离编辑页验收')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.evaluate(async () => {
    const client = await import('/src/api/client.ts'), {mockFetch} = await import('/src/api/mock.ts')
    window.__editorWrites = []
    window.__editorOriginal = await client.request('/tasks/task-demo')
    client.setTransport(async (input, init = {}) => {
      if ((init.method || 'GET') !== 'GET') {
        const body = init.body ? JSON.parse(String(init.body)) : undefined
        window.__editorWrites.push({path: String(input), method: init.method, body})
        if (body?.task) window.__savedEditorTask = body.task
      }
      return mockFetch(input, init)
    })
  })
  await open('/tasks/task-demo/edit')
  assert(await page.locator('#basic').isVisible())
  for (const panel of ['measure', 'schedule', 'advanced-settings']) assert(!(await page.locator('#' + panel).isVisible()))
  await resetScroll()
  assert(await footerVisible(), '首屏保存按钮应该可达')
  await page.locator('#name').fill('分区编辑验收')
  await page.locator('#editor-step-basic').focus()
  await page.keyboard.press('ArrowRight')
  assert.equal(await page.evaluate(() => document.activeElement.id), 'editor-step-measure')
  assert.equal(await page.locator('#editor-step-measure').getAttribute('aria-selected'), 'true')
  assert(await page.locator('#measure').isVisible())
  await page.keyboard.press('Tab')
  assert.equal(await page.evaluate(() => document.activeElement.id), 'probe_mode')
  await page.locator('#latency_max_ms').fill('222')
  await choose('schedule')
  await page.getByLabel('计划类型', {exact: true}).selectOption('interval')
  await page.locator('#interval_minutes').fill('17')
  await choose('advanced', 'download')
  assert(await page.locator('#download_url').isVisible())
  assert(!(await page.locator('#probe_count').isVisible()))
  await choose('advanced', 'budget')
  assert(await page.locator('#sample_size').isVisible())
  await choose('advanced', 'delivery')
  assert(await page.locator('#targets').isVisible())
  await choose('basic')
  assert.equal(await page.locator('#name').inputValue(), '分区编辑验收')
  assert.equal(await page.locator('#latency_max_ms').inputValue(), '222')
  assert.equal(await page.locator('#interval_minutes').inputValue(), '17')
  assert.equal((await state()).writes.length, 0, '分区切换不得自动保存或触发业务')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL('**/#/tasks/task-demo/results')
  const {task, writes} = await state(), original = await page.evaluate(() => window.__editorOriginal)
  assert.equal(writes.length, 1)
  assert.deepEqual(task.config, {...original.config, latency_max_ms: 222})
  assert.deepEqual(task.targets, original.targets)
  assert.deepEqual(task.notifications, original.notifications)
  assert.equal(task.dns_auto, original.dns_auto)
  assert.equal(task.schedule.enabled, original.schedule.enabled)
  assert.equal(task.schedule.interval_minutes, 17)
  await open('/tasks/task-demo/edit')
  await choose('advanced', 'measurement')
  await page.locator('#probe_count').fill('0')
  await choose('basic')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  const summary = page.getByRole('alert').filter({hasText: '配置未保存，请修正以下内容'})
  await summary.waitFor()
  assert(await summary.evaluate(element => document.activeElement === element))
  assert(await page.locator('#probe_count').isVisible(), '跨区错误自动显示对应分区')
  await summary.getByRole('link', {name: '请输入 1～100 的整数', exact: true}).focus()
  await page.keyboard.press('Enter')
  assert.equal(await page.evaluate(() => document.activeElement.id), 'probe_count')
  await page.locator('#probe_count').fill(String(original.config.probe_count))
  await choose('advanced', 'download')
  await page.locator('#download_url').fill('bad-url')
  await choose('basic')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.locator('#download_url').waitFor({state: 'visible'})
  assert.equal(await page.locator('#advanced-tab-download').getAttribute('aria-selected'), 'true')
  assert.equal((await state()).writes.length, 1, '隐藏无效字段仍应阻断保存')
  await page.locator('#download_url').fill(original.config.download_url)
  await choose('schedule')
  await page.locator('#interval_minutes').fill('0')
  await choose('advanced', 'budget')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.locator('#interval_minutes').waitFor({state: 'visible'})
  assert.equal(await page.locator('#editor-step-schedule').getAttribute('aria-selected'), 'true')
  await page.locator('#interval_minutes').fill('17')
  await choose('basic')
  const sourceCount = page.locator('#source-count-1')
  await sourceCount.fill('0')
  await choose('schedule')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await sourceCount.waitFor({state: 'visible'})
  await sourceCount.fill('')
  await choose('advanced', 'budget')
  await page.locator('#sample_size').fill('0')
  await choose('basic')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.locator('#sample_size').waitFor({state: 'visible'})
  assert.equal(await page.locator('#advanced-tab-budget').getAttribute('aria-selected'), 'true')
  await page.locator('#sample_size').fill(String(original.config.sample_size))
  await choose('advanced', 'delivery')
  await page.locator('#wecom').selectOption('')
  await choose('basic')
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.locator('#wecom').waitFor({state: 'visible'})
  assert.equal(await page.locator('#advanced-tab-delivery').getAttribute('aria-selected'), 'true')
  assert.equal((await state()).writes.length, 1)
  await page.locator('#wecom').selectOption(original.notifications.wecom.connection_id)
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.waitForURL('**/#/tasks/task-demo/results')
  assert.equal((await state()).writes.length, 2)

  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({width, height: 900})
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme})
      await open('/tasks/new')
      for (const [section, advanced, panel] of [['basic', '', 'basic'], ['measure', '', 'measure'], ['schedule', '', 'schedule'], ['advanced', 'measurement', 'advanced-measure'], ['advanced', 'download', 'download-settings'], ['advanced', 'budget', 'budgets'], ['advanced', 'delivery', 'delivery']]) {
        await choose(section, advanced)
        await resetScroll()
        const layout = await page.evaluate(panel => {
          const container = innerWidth > 800 ? document.getElementById('main-content') : document.scrollingElement
          const card = document.getElementById(panel), title = card.querySelector(':scope > h2, :scope > legend')
          const cardBounds = card.getBoundingClientRect(), titleBounds = title.getBoundingClientRect()
          const nextBounds = title.nextElementSibling.getBoundingClientRect()
          const titleLayout = {top: titleBounds.top - cardBounds.top, left: titleBounds.left - cardBounds.left, right: cardBounds.right - titleBounds.right, bottom: cardBounds.bottom - titleBounds.bottom, contentGap: nextBounds.top - titleBounds.bottom}
          return {overflow: document.documentElement.scrollWidth > innerWidth, scrollHeight: container.scrollHeight, viewport: container.clientHeight, visiblePanels: [...document.querySelectorAll('.editor-section')].filter(element => element.getClientRects().length).map(element => element.id), panel, titleLayout}
        }, panel)
        assert.equal(layout.overflow, false)
        assert.deepEqual(layout.visiblePanels, [panel])
        assert(layout.titleLayout.top >= 12 && layout.titleLayout.left >= 12 && layout.titleLayout.right >= 12 && layout.titleLayout.bottom >= 12 && layout.titleLayout.contentGap >= 12, '分区标题应位于卡片内，且不贴边或压住正文：' + JSON.stringify({width, colorScheme, panel, ...layout.titleLayout}))
        assert(await footerVisible(), '保存操作始终可达：' + [width, section, advanced])
        if (width === 1440 && section !== 'advanced') assert(layout.scrollHeight < 1000, '常用分区不应成为长页')
        layouts.push({width, colorScheme, ...layout})
        if (colorScheme === 'light' && ((width === 1440 && ['basic', 'schedule'].includes(section)) || (width === 390 && section === 'schedule'))) await page.screenshot({path: join(output, panel + '-' + width + '.jpg'), type: 'jpeg', quality: 90})
        if (panel === 'delivery' && [1440, 390].includes(width)) await page.screenshot({path: join(output, 'delivery-' + width + '-' + colorScheme + '.jpg'), type: 'jpeg', quality: 90})
      }
    }
  }
  await page.setViewportSize({width: 320, height: 900})
  await open('/tasks/new')
  await choose('basic')
  for (let index = 0; index < 10; index++) await page.getByRole('button', {name: '添加来源', exact: true}).click()
  await page.evaluate(async () => { const {scrollOwner} = await import('/src/lib/scroll.ts'); scrollOwner().scrollTo({top: 1000, behavior: 'instant'}) })
  assert(await footerVisible(), '多来源长内容仍可直接保存')
  assert.deepEqual(errors, [])
  assert.deepEqual(outside, [])
  console.log(JSON.stringify({status: 'passed',keyboardTabs: true,retainsEdits: true,savePreservesAdvanced: true,errorRoutes: ['measurement','download','schedule','basic','budget','delivery'],stickySave: true,panelTitlesInsideCards: true,layouts,externalRequests: 0,pageErrors: 0,note: '仅隔离替身；无真实运行、DNS 或通知'}))
} catch (error) {
  await page?.screenshot({path: join(output, 'failure.jpg'), type: 'jpeg', quality: 85}).catch(() => {})
  console.error('编辑页分区验收未完成：', error, diagnostics)
  throw error
} finally {
  await browser?.close()
  if (server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (server.exitCode === null) { server.kill('SIGKILL'); await stopped } }
}
