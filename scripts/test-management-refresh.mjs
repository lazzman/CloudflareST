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
const output = process.env.REFRESH_OUTPUT_DIR || join(web, 'test-results/refresh')
const listener = createServer().listen(0, '127.0.0.1')
await once(listener, 'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:' + port
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {cwd: web, stdio: ['ignore', 'pipe', 'pipe']})
let diagnostics = '', browser, page
const errors = [], outside = [], cases = []
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
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离自动刷新验收')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.evaluate(async () => {
    const client = await import('/src/api/client.ts'), {mockFetch} = await import('/src/api/mock.ts')
    window.__refresh = {started: 0, done: 0, pending: 0, writes: [], fail: false, rename: '', tall: false}
    client.setTransport(async (input, init = {}) => {
      const path = String(input), method = init.method || 'GET', audit = window.__refresh
      if (method !== 'GET') audit.writes.push({path, method})
      if (path !== '/api/v1/tasks?view=strategies' || method !== 'GET') return mockFetch(input, init)
      audit.started++; audit.pending++
      try {
        const response = await mockFetch(input, init), tasks = await response.json()
        await new Promise(resolve => setTimeout(resolve, 450))
        if (audit.fail) return new Response(JSON.stringify({code: 'READ_FAILED', message: '隔离样例：刷新读取失败'}), {status: 503, headers: {'Content-Type': 'application/json'}})
        if (audit.rename) tasks[0].name = audit.rename
        if (audit.tall) for (let index = 0; index < 25; index++) tasks.push({...structuredClone(tasks[0]), id: 'extra-' + index, name: '本地策略 ' + index})
        return new Response(JSON.stringify(tasks), {headers: {'Content-Type': 'application/json'}})
      } finally { audit.pending--; audit.done++ }
    })
    const {router} = await import('/src/router.ts')
    await router.push('/queue')
    await router.push('/tasks')
  })
  await page.locator('[data-resource-loading]').first().waitFor()
  await page.waitForFunction(() => window.__refresh.done >= 1 && !document.querySelector('[data-resource-loading]'))
  await page.locator('.strategy-table').waitFor()
  const startSampling = () => page.evaluate(() => {
    window.__refreshSamples = []
    window.__refreshSampling = true
    window.__refreshRow = document.querySelector('.strategy-table tbody tr')
    const collect = () => {
      if (!window.__refreshSampling) return
      const main = document.getElementById('main-content'), panel = main.querySelector('.m-surface'), table = main.querySelector('.strategy-table'), row = main.querySelector('.strategy-table tbody tr')
      if (panel && table) window.__refreshSamples.push({top: panel.getBoundingClientRect().top, tableTop: table.getBoundingClientRect().top, width: panel.getBoundingClientRect().width, scroll: innerWidth > 800 ? main.scrollTop : document.scrollingElement.scrollTop, sameRow: row === window.__refreshRow, loading: !!main.querySelector('[data-resource-loading]')})
      requestAnimationFrame(collect)
    }
    collect()
  })
  const stopSampling = () => page.evaluate(() => {
    window.__refreshSampling = false
    const samples = window.__refreshSamples
    const spread = key => Math.max(...samples.map(item => item[key])) - Math.min(...samples.map(item => item[key]))
    return {samples: samples.length, topShift: spread('top'), tableShift: spread('tableTop'), widthShift: spread('width'), scrollShift: spread('scroll'), rowPreserved: samples.every(item => item.sameRow), loadingFrames: samples.filter(item => item.loading).length}
  })
  for (const width of [1440, 390, 320]) {
    await page.setViewportSize({width, height: 900})
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme})
      await page.waitForFunction(() => !window.__refresh.pending)
      await page.getByRole('searchbox', {name: '查找名称／ID'}).fill('边缘节点')
      await page.locator('a.strategy-link').scrollIntoViewIfNeeded()
      await page.locator('a.strategy-link').focus()
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      const before = await page.evaluate(() => window.__refresh.done)
      await startSampling()
      await delay(5400)
      const result = await stopSampling()
      const polls = await page.evaluate(() => window.__refresh.done) - before
      console.log(JSON.stringify({case: '自动刷新稳定布局', width, colorScheme, polls, ...result}))
      assert(polls >= 2, '必须实际经历至少两次自动刷新，不得关闭轮询规避抖动')
      assert(result.topShift < 1 && result.tableShift < 1 && result.widthShift < 1 && result.scrollShift < 1 && result.rowPreserved, '自动刷新不应移动或重建现有列表：' + JSON.stringify(result))
      assert.equal(result.loadingFrames, 0, '已有数据时后台刷新不应切回初始加载状态')
      assert.equal(await page.getByRole('searchbox', {name: '查找名称／ID'}).inputValue(), '边缘节点')
      assert(await page.locator('a.strategy-link').evaluate(element => element === document.activeElement), '刷新应保留平铺操作的焦点')
      assert(await page.locator('button.strategy-link').isVisible(), '刷新不应隐藏归档操作')
      cases.push({width, colorScheme, polls, ...result})
    }
  }
  await page.setViewportSize({width: 1440, height: 900})
  await page.emulateMedia({colorScheme: 'light'})
  await page.waitForFunction(() => innerWidth === 1440 && document.documentElement.dataset.theme === 'light')
  await page.getByRole('button', {name: '刷新', exact: true}).scrollIntoViewIfNeeded()
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
  await page.waitForFunction(() => !window.__refresh.pending)
  await startSampling()
  const manualBefore = await page.evaluate(() => window.__refresh.done)
  await page.getByRole('button', {name: '刷新', exact: true}).click()
  await page.locator('#main-content [data-resource-loading]').waitFor()
  await page.waitForFunction(before => window.__refresh.done > before && !document.querySelector('#main-content [data-resource-loading]'), manualBefore)
  const manual = await stopSampling()
  console.log(JSON.stringify({case: '手动刷新稳定布局', ...manual}))
  assert(manual.samples > 2 && manual.topShift < 1 && manual.tableShift < 1 && manual.widthShift < 1 && manual.rowPreserved, '手动刷新也应保持内容位置：' + JSON.stringify(manual))
  await page.evaluate(() => { window.__refresh.rename = '边缘节点 · 更新已送达' })
  await page.getByRole('link', {name: '边缘节点 · 更新已送达', exact: true}).waitFor()
  const readAt = await page.locator('#main-content').getByText('最后读取：', {exact: false}).innerText()
  await page.evaluate(() => { window.__refresh.fail = true })
  const failure = page.getByRole('alert').filter({hasText: '隔离样例：刷新读取失败'})
  await failure.waitFor()
  assert(await page.getByRole('link', {name: '边缘节点 · 更新已送达', exact: true}).isVisible(), '刷新失败保留已有数据并明确过时')
  assert.equal(await page.locator('#main-content').getByText('最后读取：', {exact: false}).innerText(), readAt)
  await page.evaluate(() => { window.__refresh.fail = false })
  await failure.getByRole('button', {name: '重新读取', exact: true}).click()
  await failure.waitFor({state: 'detached'})
  await page.getByRole('searchbox', {name: '查找名称／ID'}).fill('')
  await page.evaluate(() => { window.__refresh.tall = true })
  await page.locator('.strategy-table tbody tr').nth(26).waitFor()
  await page.evaluate(async () => { const {scrollOwner} = await import('/src/lib/scroll.ts'); scrollOwner().scrollTo({top: 600, behavior: 'instant'}) })
  await page.waitForFunction(() => !window.__refresh.pending)
  await startSampling(); await delay(3000)
  const scrolled = await stopSampling()
  assert(scrolled.scrollShift < 1 && scrolled.rowPreserved, '长列表自动刷新应保持滚动位置')
  assert.deepEqual(await page.evaluate(() => window.__refresh.writes), [])
  assert.deepEqual(errors, [])
  assert.deepEqual(outside, [])
  console.log(JSON.stringify({status: 'passed', cases, manual, scrolled, stillPolls: true, dataUpdates: true, failureVisibleAndRecoverable: true, statePreserved: true, externalRequests: 0, businessWrites: 0}))
} catch (error) {
  await page?.screenshot({path: join(output, 'failure.jpg'), type: 'jpeg', quality: 85}).catch(() => {})
  console.error('自动刷新稳定性验收未完成：', error, diagnostics)
  throw error
} finally {
  await browser?.close()
  if (server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (server.exitCode === null) { server.kill('SIGKILL'); await stopped } }
}
