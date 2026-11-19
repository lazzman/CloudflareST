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
const require = createRequire(join(web, 'package.json'))
const {chromium} = require('playwright')
const output = process.env.SCROLL_OUTPUT_DIR || join(web, 'test-results/scroll')
const observe = process.env.SCROLL_OBSERVE_ONLY === '1'
const listener = createServer().listen(0, '127.0.0.1')
await once(listener, 'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:' + port
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {cwd: web, stdio: ['ignore', 'pipe', 'pipe']})
let diagnostics = '', browser, page
const errors = [], outside = [], failures = [], findings = []
server.stdout.on('data', value => { diagnostics += value })
server.stderr.on('data', value => { diagnostics += value })
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
  const metrics = () => page.evaluate(() => {
    const content = document.querySelector('.workspace > #main-content')
    const internal = !!content && innerWidth > 800 && ['auto', 'hidden'].includes(getComputedStyle(content).overflowY)
    const owner = internal ? content : document.scrollingElement
    const bounds = selector => { const element = document.querySelector(selector); if (!element) return null; const rect = element.getBoundingClientRect(); return {top: rect.top, bottom: rect.bottom, height: rect.height} }
    return {owner: internal ? 'content' : 'document', top: owner.scrollTop, max: owner.scrollHeight - owner.clientHeight, documentTop: document.scrollingElement.scrollTop, documentHeight: document.documentElement.scrollHeight, viewport: {width: innerWidth, height: innerHeight}, shell: bounds('.shell'), header: bounds('.topbar'), rail: bounds('.sidebar'), content: internal ? bounds('#main-content') : {top: 0, bottom: innerHeight, height: innerHeight}, horizontal: document.documentElement.scrollWidth > innerWidth, rootOverflow: document.documentElement.style.overflow}
  })
  const scroll = top => page.evaluate(top => { const content = document.querySelector('.workspace > #main-content'); const owner = content && innerWidth > 800 && getComputedStyle(content).overflowY === 'auto' ? content : document.scrollingElement; owner.scrollTo({top, behavior: 'instant'}) }, top)
  const waitIdle = () => page.waitForFunction(() => ![...document.querySelectorAll('[role="status"]')].some(element => element.textContent.includes('正在读取服务端状态')))
  const open = async (path, title) => {
    await page.goto(origin + '/#' + path)
    await page.waitForFunction(title => document.title === title + ' · CloudflareST', title)
    await waitIdle()
    if (title === '新建策略') {
      await page.locator('#editor-step-basic').click()
      for (let count = await page.locator('.source-row').count(); count < 13; count++) await page.getByRole('button', {name: '添加来源', exact: true}).click()
      await scroll(0)
    }
  }
  const check = (name, passed, detail) => { findings.push({case: name, passed, ...detail}); console.log(JSON.stringify(findings.at(-1))); if (!passed) failures.push(name) }
  const inContent = async selector => {
    const layout = await metrics(), box = await page.locator(selector).boundingBox()
    return !!box && box.y >= layout.content.top - 1 && box.y + Math.min(box.height, 44) <= layout.content.bottom + 1
  }
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离滚动测试')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await open('/tasks/new', '新建策略')
  const initial = await metrics()
  await page.mouse.move(1100, 700)
  await page.mouse.wheel(0, 850)
  await delay(250)
  const moved = await metrics()
  check('desktop-scroll-owner', moved.owner === 'content' && moved.top > 500 && moved.documentTop === 0 && moved.documentHeight <= 901 && Math.abs(moved.header.top - initial.header.top) < 1 && Math.abs(moved.rail.top - initial.rail.top) < 1, {initial, moved})
  const scrollbarStyle = () => page.evaluate(() => {
    const content = document.getElementById('main-content')
    const rail = getComputedStyle(content, '::-webkit-scrollbar')
    const thumb = getComputedStyle(content, '::-webkit-scrollbar-thumb')
    const track = getComputedStyle(content, '::-webkit-scrollbar-track')
    return {width: rail.width, color: thumb.backgroundColor, radius: thumb.borderRadius, border: thumb.borderLeftWidth, clip: thumb.backgroundClip, track: track.backgroundColor, gutter: content.offsetWidth - content.clientWidth, standardWidth: getComputedStyle(content).scrollbarWidth, standardColor: getComputedStyle(content).scrollbarColor}
  })
  await page.mouse.move(100, 300)
  const lightScrollbar = await scrollbarStyle()
  check('scrollbar-light-style', lightScrollbar.width === '10px' && lightScrollbar.radius === '999px' && lightScrollbar.border === '3px' && lightScrollbar.clip === 'padding-box' && lightScrollbar.track === 'rgba(0, 0, 0, 0)' && lightScrollbar.gutter === 10, lightScrollbar)
  await page.emulateMedia({colorScheme: 'dark'})
  await page.waitForFunction(() => document.documentElement.dataset.theme === 'dark')
  const darkScrollbar = await scrollbarStyle()
  check('scrollbar-dark-style', darkScrollbar.width === lightScrollbar.width && darkScrollbar.color !== lightScrollbar.color && darkScrollbar.track === lightScrollbar.track, darkScrollbar)
  await page.emulateMedia({colorScheme: 'light'})
  await page.waitForFunction(() => document.documentElement.dataset.theme === 'light')
  await scroll(0)
  await delay(150)
  const handle = await page.locator('#main-content').evaluate(element => {
    const box = element.getBoundingClientRect()
    return {x: box.right - 5, y: box.top + Math.max(36, element.clientHeight * element.clientHeight / element.scrollHeight) / 2}
  })
  await page.screenshot({path: join(output, 'scrollbar-before-drag.jpg'), type: 'jpeg', quality: 85})
  await page.mouse.move(handle.x, handle.y)
  await delay(100)
  await page.mouse.down()
  await page.mouse.move(handle.x, handle.y + 150, {steps: 8})
  await page.mouse.up()
  await delay(100)
  check('scrollbar-native-drag', (await metrics()).top > 100 && (await scrollbarStyle()).gutter === lightScrollbar.gutter, {handle, top: (await metrics()).top, gutter: (await scrollbarStyle()).gutter})
  await scroll(0)
  await page.locator('#main-content').focus()
  await page.keyboard.press('PageDown')
  await delay(200)
  check('scrollbar-keyboard-page-down', (await metrics()).top > 100, {top: (await metrics()).top})
  await page.emulateMedia({forcedColors: 'active'})
  const accessibleScrollbar = await scrollbarStyle()
  check('scrollbar-forced-colors-native', accessibleScrollbar.standardWidth === 'auto' && accessibleScrollbar.standardColor === 'auto' && accessibleScrollbar.width !== '10px', accessibleScrollbar)
  await page.emulateMedia({forcedColors: 'none'})
  await scroll(moved.top)
  await page.mouse.move(1100, 700)
  await page.screenshot({path: join(output, 'desktop-scrolled.jpg'), type: 'jpeg', quality: 85})
  await page.locator('#editor-step-advanced').click(); await page.locator('#advanced-tab-delivery').click(); await page.locator('#delivery').scrollIntoViewIfNeeded()
  await delay(100)
  const anchored = await metrics(), target = await page.locator('#delivery').boundingBox()
  check('section-switch-visible', !!target && target.y >= anchored.header.bottom + 6 && target.y >= anchored.content.top && target.y < anchored.content.bottom - 44 && page.url().includes('#/tasks/new'), {targetY: target?.y, headerBottom: anchored.header.bottom, contentTop: anchored.content.top, route: new URL(page.url()).hash})
  await page.getByRole('button', {name: '保存策略', exact: true}).click()
  await page.getByRole('heading', {name: '配置未保存，请修正以下内容', exact: true}).waitFor()
  check('validation-summary-visible', await inContent('[role="alert"][tabindex="-1"]'), {layout: await metrics()})
  await page.locator('[role="alert"][tabindex="-1"] a').first().click()
  check('invalid-field-visible', await page.locator('#name').evaluate(element => document.activeElement === element) && await inContent('#name'), {})
  await open('/connections', '服务连接')
  await scroll(1200)
  const beforeModal = await metrics()
  await page.getByRole('button', {name: '新增连接', exact: true}).click()
  let dialog = page.getByRole('dialog', {name: '新增连接', exact: true})
  await dialog.waitFor()
  await page.mouse.move(1430, 880)
  await page.mouse.wheel(0, 600)
  await delay(150)
  check('modal-background-locked', Math.abs((await metrics()).top - beforeModal.top) < 2, {before: beforeModal.top, after: (await metrics()).top})
  await dialog.getByRole('button', {name: '关闭', exact: true}).click()
  check('modal-close-keeps-position', Math.abs((await metrics()).top - beforeModal.top) < 2, {before: beforeModal.top, after: (await metrics()).top})
  await page.getByRole('navigation', {name: '主导航', exact: true}).getByRole('link', {name: '系统设置', exact: true}).click()
  await page.getByRole('heading', {name: '系统设置', exact: true}).waitFor()
  await waitIdle()
  check('new-route-starts-at-top', (await metrics()).top < 2, {layout: await metrics()})
  await page.evaluate(async () => {
    const client = await import('/src/api/client.ts'), mock = await import('/src/api/mock.ts')
    client.setTransport(async (input, init) => { if (String(input).includes('/tasks/task-demo/runs/run-demo-001')) await new Promise(resolve => setTimeout(resolve, 180)); return mock.mockFetch(input, init) })
  })
  await open('/tasks/task-demo/runs/run-demo-001', '执行详情')
  await scroll(1100)
  const saved = (await metrics()).top
  await page.getByRole('navigation', {name: '主导航', exact: true}).getByRole('link', {name: '系统设置', exact: true}).click()
  await page.getByRole('heading', {name: '系统设置', exact: true}).waitFor()
  await page.goBack()
  await page.waitForFunction(() => document.title === '执行详情 · CloudflareST')
  await waitIdle()
  await delay(350)
  check('back-restores-after-data', saved > 900 && Math.abs((await metrics()).top - saved) < 3, {expected: saved, actual: (await metrics()).top})
  await delay(2200)
  check('polling-keeps-position', Math.abs((await metrics()).top - saved) < 3, {expected: saved, actual: (await metrics()).top})
  await page.evaluate(async () => { const {router} = await import('/src/router.ts'); await router.replace({query: {scroll_probe: 'same-page'}}) })
  await delay(100)
  check('same-route-query-keeps-position', Math.abs((await metrics()).top - saved) < 3, {expected: saved, actual: (await metrics()).top})
  await page.getByRole('navigation', {name: '主导航', exact: true}).getByRole('link', {name: '系统设置', exact: true}).click()
  await page.getByRole('heading', {name: '系统设置', exact: true}).waitFor()
  await page.goBack()
  await page.waitForFunction(() => document.title === '执行详情 · CloudflareST')
  await page.mouse.move(1100, 600)
  await page.mouse.wheel(0, 200)
  await waitIdle()
  await delay(250)
  check('user-scroll-cancels-pending-restore', (await metrics()).top < saved - 100, {previous: saved, afterUserScroll: (await metrics()).top})
  await page.setViewportSize({width: 1280, height: 420})
  await open('/tasks/new', '新建策略')
  await page.getByRole('button', {name: '退出登录', exact: true}).scrollIntoViewIfNeeded()
  const logout = await page.getByRole('button', {name: '退出登录', exact: true}).boundingBox(), short = await metrics()
  check('short-desktop-sidebar', !!logout && logout.y >= 0 && logout.y + logout.height <= 420 && short.documentTop === 0, {logout, layout: short})
  const desktopNested = await page.evaluate(async () => {
    const {lockBackgroundScroll} = await import('/src/lib/scroll.ts')
    const navigation = document.querySelector('.sidebar nav'), main = document.getElementById('main-content')
    const before = {nav: navigation.style.overflow, main: main.style.overflow, root: document.documentElement.style.overflow}
    const first = lockBackgroundScroll(), second = lockBackgroundScroll()
    first()
    const navigationLocked = getComputedStyle(navigation).overflowY === 'hidden'
    const mainLocked = getComputedStyle(main).overflowY === 'hidden'
    second()
    return {navigationLocked, mainLocked, restored: navigation.style.overflow === before.nav && main.style.overflow === before.main && document.documentElement.style.overflow === before.root}
  })
  check('desktop-nested-locks-include-navigation', desktopNested.navigationLocked && desktopNested.mainLocked && desktopNested.restored, desktopNested)
  await page.screenshot({path: join(output, 'short-window.jpg'), type: 'jpeg', quality: 85})
  for (const width of [390, 320]) {
    await page.setViewportSize({width, height: 844})
    await open('/tasks/new', '新建策略')
    await page.locator('#editor-step-advanced').click(); await page.locator('#advanced-tab-delivery').click(); await page.locator('#delivery').scrollIntoViewIfNeeded()
    await delay(100)
    const mobile = await metrics(), fieldset = await page.locator('#delivery').boundingBox()
    check('mobile-section-switch-' + width, mobile.owner === 'document' && !!fieldset && fieldset.y < 844 && fieldset.y + fieldset.height > 0 && !mobile.horizontal, {targetY: fieldset?.y, layout: mobile})
    await scroll(100000)
    check('mobile-save-reachable-' + width, await inContent('.form-footer') && !(await metrics()).horizontal, {})
    await open('/connections', '服务连接')
    await scroll(300)
    const mobileBeforeModal = await metrics()
    await page.getByRole('button', {name: '新增连接', exact: true}).evaluate(element => element.click())
    dialog = page.getByRole('dialog', {name: '新增连接', exact: true})
    await dialog.waitFor()
    const dialogBounds = await dialog.boundingBox()
    await page.mouse.move(width - 2, 830)
    await page.mouse.wheel(0, 600)
    await delay(150)
    check('mobile-modal-lock-' + width, Math.abs((await metrics()).documentTop - mobileBeforeModal.documentTop) < 2 && (await metrics()).rootOverflow === 'hidden' && !!dialogBounds && dialogBounds.y >= 0 && dialogBounds.y + dialogBounds.height <= 844, {before: mobileBeforeModal.documentTop, after: (await metrics()).documentTop, dialogBounds})
    await page.keyboard.press('Escape')
    await dialog.waitFor({state: 'detached'})
    const unlocked = await metrics()
    await page.mouse.move(width / 2, 700)
    await page.mouse.wheel(0, 300)
    await delay(150)
    check('mobile-modal-unlock-' + width, unlocked.rootOverflow === '' && Math.abs(unlocked.documentTop - mobileBeforeModal.documentTop) < 2 && (await metrics()).documentTop > unlocked.documentTop + 100, {unlocked: unlocked.documentTop, afterWheel: (await metrics()).documentTop})
    await page.screenshot({path: join(output, 'mobile-' + width + '.jpg'), type: 'jpeg', quality: 85})
  }
  const nested = await page.evaluate(async () => {
    const {lockBackgroundScroll, scrollOwner} = await import('/src/lib/scroll.ts')
    const before = document.documentElement.style.overflow
    const first = lockBackgroundScroll(), second = lockBackgroundScroll()
    const ownerIsDocument = scrollOwner() === document.scrollingElement
    first(); first()
    const staysLocked = document.documentElement.style.overflow === 'hidden'
    second()
    return {ownerIsDocument, staysLocked, restored: document.documentElement.style.overflow === before}
  })
  check('nested-locks-preserve-document-owner', nested.ownerIsDocument && nested.staysLocked && nested.restored, nested)
  await page.setViewportSize({width: 2048, height: 954})
  await page.evaluate(async () => {
    const {setTransport} = await import('/src/api/client.ts'), {mockFetch} = await import('/src/api/mock.ts')
    setTransport(async (input, init) => {
      const response = await mockFetch(input, init)
      if (!String(input).includes('/runs/run-demo-001/candidates')) return response
      const value = await response.json()
      value.items = Array.from({length: 50}, (_, index) => ({...value.items[0], ip: '192.0.2.' + (index + 1), latency_ms: 30 + index / 10}))
      value.total = 261
      return new Response(JSON.stringify(value), {status: response.status, headers: response.headers})
    })
  })
  await open('/tasks/task-demo/results', 'IP 结果')
  await page.locator('.result-table tbody tr').nth(49).waitFor()
  for (const theme of ['light', 'dark']) {
    await page.emulateMedia({colorScheme: theme})
    await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, theme)
    await scroll(0)
    await page.mouse.move(100, 300)
    const resultLayout = await metrics()
    check('result-scrollbar-' + theme, resultLayout.owner === 'content' && resultLayout.max > 500 && !resultLayout.horizontal && (await scrollbarStyle()).gutter === 10, {layout: resultLayout, style: await scrollbarStyle()})
    await page.screenshot({path: join(output, 'result-scrollbar-' + theme + '.jpg'), type: 'jpeg', quality: 90})
    const bounds = await page.locator('#main-content').boundingBox()
    await page.screenshot({path: join(output, 'scrollbar-closeup-' + theme + '.jpg'), type: 'jpeg', quality: 95, clip: {x: bounds.x + bounds.width - 280, y: bounds.y, width: 280, height: Math.min(600, bounds.height)}})
  }
  assert.deepEqual(errors, [])
  assert.deepEqual(outside, [])
  console.log(JSON.stringify({status: failures.length ? 'failed' : 'passed', failures, browser: browser.version(), checks: findings.length, externalRequests: outside.length, pageErrors: errors.length, note: '仅本机替身、无测速或真实DNS/通知；observe模式仅用于记录修复前现象'}))
  if (!observe) assert.equal(failures.length, 0, failures.join('、'))
} catch (error) {
  await page?.screenshot({path: join(output, 'failure.jpg'), type: 'jpeg', quality: 85}).catch(() => {})
  console.error('滚动复验未完成：', error, diagnostics)
  throw error
} finally {
  await browser?.close()
  if (server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (server.exitCode === null) { server.kill('SIGKILL'); await stopped } }
}
