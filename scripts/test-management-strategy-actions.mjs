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
const output = process.env.ACTIONS_OUTPUT_DIR || join(web, 'test-results/strategy-actions')
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
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码', {exact: true}).fill('仅隔离按钮验收')
  await page.getByRole('button', {name: '登录管理端', exact: true}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.locator('.strategy-table').waitFor()
  await page.evaluate(async () => {
    const client = await import('/src/api/client.ts'), {mockFetch} = await import('/src/api/mock.ts')
    window.__actionWrites = []
    client.setTransport(async (input, init = {}) => {
      if ((init.method || 'GET') !== 'GET') window.__actionWrites.push({path: String(input), method: init.method, body: init.body ? JSON.parse(String(init.body)) : undefined})
      return mockFetch(input, init)
    })
  })
  const row = page.locator('.strategy-table tbody tr').filter({has: page.getByRole('link', {name: '边缘节点 · 每日复核', exact: true})})
  const secondRow = page.locator('.strategy-table tbody tr').filter({has: page.getByRole('link', {name: 'IPv6 · 每周抽样', exact: true})})
  const advanced = row.getByRole('link', {name: '高级管理', exact: true})
  const archiveButton = row.getByRole('button', {name: '归档策略', exact: true})
  const writes = () => page.evaluate(() => window.__actionWrites)
  for (const width of [1440, 1280, 390, 320]) {
    await page.setViewportSize({width, height: 900})
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    for (const colorScheme of ['light', 'dark']) {
      await page.emulateMedia({colorScheme})
      assert.equal(await page.locator('.row-more, .strategy-menu, [popovertarget]').count(), 0, '策略操作不再折叠到更多菜单')
      for (const control of [row.getByRole('link', {name: '查看结果', exact: true}), row.getByRole('button', {name: '运行一次', exact: true}), row.getByRole('link', {name: '编辑', exact: true}), row.getByRole('switch'), advanced, archiveButton]) {
        assert(await control.isVisible(), '所有操作无需展开即可显示')
        await control.scrollIntoViewIfNeeded()
        assert(await control.evaluate(element => {
          const box = element.getBoundingClientRect()
          return box.width > 24 && box.height >= 34 && element.scrollWidth <= element.clientWidth && element.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2))
        }), '按钮文字完整且可点击，不被裁切或遮挡')
      }
      const styles = await row.locator('.strategy-link').evaluateAll(elements => elements.map(element => {
        const style = getComputedStyle(element), bounds = element.getBoundingClientRect()
        return {fontSize: style.fontSize, height: bounds.height, padding: style.padding}
      }))
      assert.equal(styles.length, 2)
      assert.deepEqual(styles[0], styles[1], '高级管理和归档使用相同字号、高度和内距')
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), '平铺操作不能造成横向溢出')
      layouts.push({width, colorScheme, rowHeight: (await row.boundingBox()).height, secondary: styles[0]})
      if ([1440, 390].includes(width)) await page.screenshot({path: join(output, 'actions-' + width + '-' + colorScheme + '.jpg'), type: 'jpeg', quality: 90})
    }
  }
  await page.setViewportSize({width: 1440, height: 900})
  await row.getByRole('switch').focus()
  await page.keyboard.press('Tab')
  assert(await advanced.evaluate(element => element === document.activeElement))
  await page.keyboard.press('Tab')
  assert(await archiveButton.evaluate(element => element === document.activeElement))
  await page.keyboard.press('Enter')
  const confirmation = page.getByRole('dialog', {name: '归档策略', exact: true})
  await confirmation.waitFor()
  assert.equal((await writes()).length, 0, '归档须二次确认')
  await confirmation.getByRole('button', {name: '取消', exact: true}).click()
  await confirmation.waitFor({state: 'detached'})
  assert(await archiveButton.evaluate(element => element === document.activeElement), '取消应回到归档按钮')
  assert.equal((await writes()).length, 0, '取消不产生业务写入')
  assert(await secondRow.getByRole('button', {name: '归档策略', exact: true}).isDisabled(), '排队／运行时保留归档禁用保护')
  const original = await page.evaluate(async () => { const {request} = await import('/src/api/client.ts'); return request('/tasks/task-demo') })
  await archiveButton.click()
  await confirmation.getByRole('button', {name: '确认', exact: true}).click()
  await confirmation.waitFor({state: 'detached'})
  await row.waitFor({state: 'detached'})
  assert.equal((await writes()).length, 1)
  assert((await writes())[0].path.endsWith('/tasks/task-demo/archive'))
  await page.getByRole('button', {name: '已归档', exact: true}).click()
  const restoreButton = row.getByRole('button', {name: '恢复策略', exact: true})
  await restoreButton.waitFor()
  assert(await advanced.isVisible())
  await restoreButton.click()
  const restore = page.getByRole('dialog', {name: '恢复策略', exact: true})
  await restore.waitFor()
  assert((await restore.innerText()).includes('定时保持关闭'))
  await restore.getByRole('button', {name: '确认', exact: true}).click()
  await row.waitFor({state: 'detached'})
  assert.equal((await writes()).length, 2)
  assert((await writes())[1].path.endsWith('/tasks/task-demo/restore'))
  const restored = await page.evaluate(async () => { const {request} = await import('/src/api/client.ts'); return request('/tasks/task-demo') })
  assert.equal(restored.schedule.enabled, false)
  assert.deepEqual(restored.config, original.config)
  assert.deepEqual(restored.targets, original.targets)
  assert.deepEqual(restored.notifications, original.notifications)
  assert.equal(restored.dns_auto, original.dns_auto)
  await page.getByRole('button', {name: '返回我的策略', exact: true}).click()
  await advanced.focus(); await page.keyboard.press('Enter')
  await page.waitForURL('**/#/tasks/task-demo')
  assert.equal((await writes()).length, 2)
  assert.deepEqual(errors, [])
  assert.deepEqual(outside, [])
  console.log(JSON.stringify({status: 'passed', layouts, alwaysVisible: true, keyboardAndFocus: true, archiveConfirmation: true, restorePreservesConfig: true, disabledProtection: true, externalRequests: 0, businessWrites: 2, note: '仅隔离替身的归档和恢复；无真实执行、DNS 或通知'}))
} catch (error) {
  await page?.screenshot({path: join(output, 'failure.jpg'), type: 'jpeg', quality: 85}).catch(() => {})
  console.error('策略平铺操作验收未完成：', error, diagnostics)
  throw error
} finally {
  await browser?.close()
  if (server.exitCode === null) { const stopped = once(server, 'exit'); server.kill('SIGTERM'); await Promise.race([stopped, delay(5000)]); if (server.exitCode === null) { server.kill('SIGKILL'); await stopped } }
}
