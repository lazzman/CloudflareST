import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createServer} from 'node:net'
import {once} from 'node:events'
import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {setTimeout as delay} from 'node:timers/promises'

const root = fileURLToPath(new URL('../', import.meta.url))
const web = join(root, 'internal/management/web')
const require = createRequire(join(web, 'package.json'))
const {chromium} = require('playwright')
const listener = createServer().listen(0, '127.0.0.1')
await once(listener, 'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:' + port
const server = spawn(process.execPath, ['node_modules/vite/bin/vite.js', '--mode', 'mock', '--host', '127.0.0.1', '--port', String(port), '--strictPort'], {cwd: web, stdio: ['ignore', 'pipe', 'pipe']})
let diagnostics = ''
server.stdout.on('data', chunk => { diagnostics += chunk.toString() })
server.stderr.on('data', chunk => { diagnostics += chunk.toString() })
let browser
const blocked = []
const errors = []
const findings = []

try {
  let ready = false
  for (let attempt = 0; attempt < 80; attempt++) {
    try { if ((await fetch(origin)).ok) { ready = true; break } } catch {}
    await delay(100)
  }
  assert(ready, '隔离页面未启动：' + diagnostics)
  browser = await chromium.launch({headless: true, args: ['--disable-background-networking', '--disable-component-update', '--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'], ...(process.env.PLAYWRIGHT_EXECUTABLE_PATH ? {executablePath: process.env.PLAYWRIGHT_EXECUTABLE_PATH} : {})})
  const context = await browser.newContext({viewport: {width: 1280, height: 900}, serviceWorkers: 'block'})
  await context.route('**/*', route => {
    if (new URL(route.request().url()).origin !== origin) { blocked.push(route.request().url()); return route.abort() }
    return route.continue()
  })
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(origin + '/#/login')
  await page.getByLabel('管理密码').fill('仅本地预览测试')
  await page.getByRole('button', {name: '登录管理端'}).click()
  await page.getByRole('heading', {name: '优选策略', exact: true}).waitFor()
  await page.evaluate(async () => {
    const client = await import('/src/api/client.ts')
    const mock = await import('/src/api/mock.ts')
    const base = await (await mock.mockFetch('/api/v1/tasks/task-demo/runs/run-demo-001')).json()
    const candidate = {...base.result.candidates[0], sources: ['source-audit-ip', 'source-audit-cidr'], download_bytes: 30000000, download_seconds: 3, download_end: 'completed'}
    base.result.candidates = [candidate, {...candidate, ip: '192.0.2.19', eligible: false, rank: 0, state: 'untested', speed_mb_s: null, download_bytes: 0, download_seconds: 0, download_end: '', steps: [{stage: 'download', state: 'skipped', reason: '未进入下载配额，未进行下载测量'}]}]
    base.result.sources = [
      {id: 'source-audit-ip', source: {kind: 'ip', value: candidate.ip}, status: 'completed', candidate_count: 1},
      {id: 'source-audit-cidr', source: {kind: 'cidr', value: '192.0.2.0/24'}, status: 'completed', candidate_count: 1},
    ]
    base.snapshot.config.download_window_seconds = 10
    const target = {...base.deliveries[0].target, protection: undefined}
    base.deliveries = [{id: 'delivery-audit-dns', kind: 'dns', connection_id: 'connection-alidns', target, ip: candidate.ip, state: 'success', verified: true, reason: '模拟 DNS 更新已确认；未发真实请求', started_at: '2026-11-14T00:01:00Z', ended_at: '2026-11-14T00:01:01Z'}]
    const rows = Array.from({length: 21}, (_, index) => ({...structuredClone(base), id: 'audit-run-' + String(index + 1).padStart(2, '0'), queued_at: new Date(Date.UTC(2026, 10, 14, 0, index)).toISOString()}))
    const current = {...structuredClone(base), id: 'audit-running', status: 'running', stage: 'probe', slot_busy: true, started_at: new Date(Date.now() - 180000).toISOString(), ended_at: undefined}
    const waiting = [1, 2].map(index => ({...structuredClone(base), id: 'audit-waiting-' + index, status: 'queued', stage: 'queued', slot_busy: false, started_at: undefined, ended_at: undefined, queued_at: new Date(Date.now() - (6 - index) * 60000).toISOString()}))
    window.__completenessReads = []
    window.__completenessWrites = []
    client.setTransport(async (input, init = {}) => {
      const url = new URL(String(input), location.origin)
      const method = init.method || 'GET'
      if (method !== 'GET') window.__completenessWrites.push({path: url.pathname, method})
      else window.__completenessReads.push(url.pathname)
      const response = value => new Response(JSON.stringify(value), {status: 200, headers: {'Content-Type': 'application/json'}})
      if (method === 'GET' && url.pathname === '/api/v1/queue') return response({current, waiting})
      if (method === 'GET' && url.pathname === '/api/v1/tasks/task-demo/runs') {
        const page = Number(url.searchParams.get('page') || 1)
        const size = Number(url.searchParams.get('page_size') || 20)
        return response({items: rows.slice((page-1)*size,page*size), total:rows.length, page, page_size:size})
      }
      if (method === 'GET' && url.pathname.endsWith('/candidates')) {
        const items = base.result.candidates.filter(item => !url.searchParams.get('eligibility') || item.eligible === (url.searchParams.get('eligibility') === 'eligible'))
        return response({items, total:items.length, page:1, page_size:50})
      }
      if (method === 'GET' && /^\/api\/v1\/tasks\/task-demo\/runs\/[^/]+(?:\/progress)?$/.test(url.pathname)) {
        const value = {...structuredClone(base), id: url.pathname.split('/')[6]}
        value.candidate_summary = {total:2, families:[{family:'ipv4', count:2, eligible:1, best:base.result.candidates[0]},{family:'ipv6',count:0,eligible:0}]}
        delete value.result.candidates
        return response(url.pathname.endsWith('/progress') ? {id:value.id,status:value.status,stage:value.stage,progress:value.progress,result_status:value.result.status,candidate_summary:value.candidate_summary} : value)
      }
      return mock.mockFetch(input, init)
    })
  })

  await page.goto(origin + '/#/tasks/task-demo/runs/run-demo-001')
  await page.getByRole('heading', {name: '候选及逐步骤判定'}).waitFor()
  const candidateRow = page.locator('tbody tr').filter({has: page.locator('code', {hasText: '192.0.2.18'})})
  await candidateRow.getByText('全部命中来源（2）', {exact:true}).click()
  const candidateText = await candidateRow.innerText()
  assert(candidateText.includes('30,000,000') && candidateText.includes('3 s'), '候选夹具未完整加载')
  findings.push({case: 'candidate-source-tags', expected: '候选可查看全部来源标记', visible: ['source-audit-ip', 'source-audit-cidr'].every(source => candidateText.includes(source))})
  findings.push({case: 'download-completion', expected: '可辨认文件提前完整下载，而不是窗口到期', visible: /提前完成|完整下载|文件.*完成|completed/.test(candidateText)})
  const untestedText = await page.locator('tbody tr').filter({has: page.locator('code', {hasText: '192.0.2.19'})}).innerText()
  findings.push({case: 'untested-download-display', expected: '未执行下载时明确未测，而非有效0字节／0秒样本', zeroSampleDisplayed: untestedText.includes('0 字节 / 0 s')})
  const deliveryText = await page.locator('.delivery-row').filter({hasText: 'delivery-audit-dns'}).innerText()
  findings.push({case: 'dns-delivery-fields', expected: '投递详情显示本次选定写入 IP 与处理时间', selectedIPVisible: deliveryText.includes('192.0.2.18'), startedAtVisible: /00:01:00|8:01:00/.test(deliveryText), actual: deliveryText})
  const reads = await page.evaluate(() => window.__completenessReads)
  findings.push({case: 'candidate-pagination-wiring', expected: '详情使用候选分页读取', candidateEndpointRequested: reads.some(value => value.endsWith('/candidates'))})

  await page.goto(origin + '/#/queue')
  await page.locator('ol.plain-list li').nth(1).waitFor()
  const queueText = await page.locator('main').innerText()
  const queueListStyle = await page.locator('ol.plain-list').evaluate(element => getComputedStyle(element).listStyleType)
  findings.push({case: 'queue-visible-progress', expected: '明确队列位置、实际等待时长、当前运行已用时', listStyle: queueListStyle, explicitPositionVisible: /第\s*1\s*位/.test(queueText), waitElapsedVisible: /已等待|等待时长/.test(queueText), runElapsedVisible: /已运行|运行已用|运行时长|耗时/.test(queueText)})

  await page.goto(origin + '/#/tasks/task-demo/runs?trigger=manual')
  await page.getByLabel('触发来源').waitFor()
  assert.equal(await page.getByLabel('触发来源').inputValue(), 'manual')
  await page.getByRole('button', {name: '下一页', exact: true}).click()
  assert((await page.locator('nav[aria-label="分页"]').innerText()).includes('第 2 / 2 页'))
  await page.getByRole('link', {name: '查看详情', exact: true}).first().click()
  await page.getByRole('heading', {name: '候选及逐步骤判定'}).waitFor()
  await page.getByRole('link', {name: /←.*运行记录/}).click()
  await page.getByLabel('触发来源').waitFor()
  findings.push({case: 'return-list-state', expected: '详情返回保留手动筛选和第2页', triggerAfterReturn: await page.getByLabel('触发来源').inputValue(), paginationAfterReturn: await page.locator('nav[aria-label="分页"]').innerText()})

  assert.deepEqual(errors, [], '页面脚本异常')
  assert.deepEqual(blocked, [], '页面尝试访问隔离范围外地址')
  assert.deepEqual(await page.evaluate(() => window.__completenessWrites), [], '本核对不得提交业务动作')
  for (const finding of findings) {
    if ('visible' in finding) assert.equal(finding.visible, true, finding.case)
    if ('zeroSampleDisplayed' in finding) assert.equal(finding.zeroSampleDisplayed, false, finding.case)
    if ('selectedIPVisible' in finding) { assert.equal(finding.selectedIPVisible, true); assert.equal(finding.startedAtVisible, true) }
    if ('candidateEndpointRequested' in finding) assert.equal(finding.candidateEndpointRequested, true)
    if ('explicitPositionVisible' in finding) { assert(finding.explicitPositionVisible); assert(finding.waitElapsedVisible); assert(finding.runElapsedVisible) }
    if ('triggerAfterReturn' in finding) { assert.equal(finding.triggerAfterReturn, 'manual'); assert(finding.paginationAfterReturn.includes('第 2 / 2 页')) }
    console.log(JSON.stringify({...finding,status:'passed'}))
  }
  console.log(JSON.stringify({case: 'isolation', browser: browser.version(), externalRequests: 0, businessWrites: 0, note: '七类缺口专项UI断言通过；不等同全部PRD验收'}))
} finally {
  if (browser) await browser.close()
  if (server.exitCode === null) {
    const exited = once(server, 'exit')
    server.kill('SIGTERM')
    await Promise.race([exited, delay(5000)])
    if (server.exitCode === null) server.kill('SIGKILL')
  }
}
