import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'node:net'
import {once} from 'node:events'
import {setTimeout as delay} from 'node:timers/promises'

const root = fileURLToPath(new URL('../../',import.meta.url))
const web = join(root,'internal/management/web')
const require = createRequire(join(web,'package.json'))
const {chromium} = require('playwright')
const listener = createServer().listen(0,'127.0.0.1')
await once(listener,'listening')
const port = listener.address().port
await new Promise(resolve => listener.close(resolve))
const origin = 'http://127.0.0.1:'+port
const server = spawn(process.execPath,['node_modules/vite/bin/vite.js','--mode','mock','--host','127.0.0.1','--port',String(port),'--strictPort'],{cwd:web,stdio:['ignore','pipe','pipe']})
let diagnostics = ''
server.stdout.on('data',chunk=>{diagnostics+=chunk.toString()})
server.stderr.on('data',chunk=>{diagnostics+=chunk.toString()})
let browser
const outside = []
const errors = []
try {
  let ready = false
  for(let attempt=0;attempt<80;attempt++) {
    try { if((await fetch(origin)).ok) {ready=true;break} } catch {}
    await delay(100)
  }
  assert(ready,diagnostics)
  browser = await chromium.launch({headless:true,args:['--disable-background-networking','--disable-component-update','--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],...(process.env.PLAYWRIGHT_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_EXECUTABLE_PATH}:{})})
  const context = await browser.newContext({viewport:{width:1280,height:900},serviceWorkers:'block'})
  await context.route('**/*',route=>{
    if(new URL(route.request().url()).origin!==origin) {outside.push(route.request().url());return route.abort()}
    return route.continue()
  })
  const page = await context.newPage()
  page.setDefaultTimeout(10000)
  page.on('pageerror',error=>errors.push(error.message))
  await page.goto(origin+'/#/login')
  await page.getByLabel('管理密码').fill('仅本地隔离审查')
  await page.getByRole('button',{name:'登录管理端'}).click()
  await page.getByRole('heading',{name:'优选策略',exact:true}).waitFor()
  await page.evaluate(async()=>{
    const client = await import('/src/api/client.ts')
    const mock = await import('/src/api/mock.ts')
    const original = await (await mock.mockFetch('/api/v1/tasks/task-demo/runs/run-demo-001')).json()
    const failure = {...original,id:'audit-failure-run',status:'failed',stage:'failed',slot_busy:false,reason:'审查运行终止原因：来源池为空',result:{status:'failed',error:'本轮全部来源均未提供已启用地址族的候选',sources:[],budgets:[],candidates:null},deliveries:[],candidate_summary:{total:0,families:[{family:'ipv4',count:0,eligible:0}]}}
    window.__auditFailure = failure
    window.__auditPostAttempts = 0
    client.setTransport(async(input,init={})=>{
      const path = new URL(String(input),location.origin).pathname
      const method = init.method || 'GET'
      const response = (value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
      if(path==='/api/v1/connections'&&method==='POST') {window.__auditPostAttempts++;throw new TypeError('模拟响应丢失，未发送任何真实请求')}
      if(path.startsWith('/api/v1/operations/')&&method==='GET')return response({code:'operation_unknown',message:'查不到原操作收据，不能认定尚未执行'},404)
      if(path==='/api/v1/tasks/task-demo/runs/audit-failure-run')return response(failure)
      if(path==='/api/v1/tasks/task-demo/runs/audit-failure-run/progress')return response({id:failure.id,status:failure.status,result_status:failure.result.status,progress:failure.progress})
      if(path==='/api/v1/tasks/task-demo/runs/audit-failure-run/candidates')return response({items:[],total:0,page:1,page_size:50})
      return mock.mockFetch(input,init)
    })
  })
  await page.goto(origin+'/#/tasks/task-demo/runs/audit-failure-run')
  await page.getByRole('heading',{name:'当次配置快照',exact:true}).waitFor()
  const facts = await page.evaluate(()=>({text:document.querySelector('main').textContent,reason:window.__auditFailure.reason,error:window.__auditFailure.result.error}))
  console.log(JSON.stringify({case:'run-failure-reason',mainStatusVisible:(await page.locator('main').innerText()).includes('失败'),terminalReasonVisible:facts.text.includes(facts.reason),optimizerErrorVisible:facts.text.includes(facts.error)}))
  await page.evaluate(()=>{
    window.__auditFailure.status = 'interrupted'
    window.__auditFailure.stage = 'interrupted'
    window.__auditFailure.reason = '服务重启，未恢复执行、补跑或重放外部操作'
    window.__auditFailure.result = {status:'',sources:[],budgets:[],candidates:null,error:''}
  })
  await page.goto(origin+'/#/queue')
  await page.getByRole('heading',{name:'运行队列',exact:true}).waitFor()
  await page.goto(origin+'/#/tasks/task-demo/runs/audit-failure-run')
  await page.getByRole('heading',{name:'当次配置快照',exact:true}).waitFor()
  const interrupted = await page.locator('main').innerText()
  console.log(JSON.stringify({case:'restart-interruption-reason',mainStatusVisible:interrupted.includes('已中断'),reasonVisible:interrupted.includes('服务重启，未恢复执行、补跑或重放外部操作'),note:'该原因文本来自后端recoverRuns实际分支；状态可见，来源与候选没有可替代原因'}))
  const lock = await page.evaluate(async()=>{
    const client = await import('/src/api/client.ts')
    const draft = name=>({connection:{kind:'telegram',name,secrets:{bot_token:'100001:FAKE_NOT_REAL_TOKEN',chat_id:'-100001'}}})
    let firstCode,lookupCode,secondCode
    try {await client.write('/connections',{body:draft('连接一')})}catch(error){firstCode=error.details.code}
    const id = client.getPendingIds()[0]
    try {await client.lookupOperation(id)}catch(error){lookupCode=error.details.code}
    try {await client.write('/connections',{body:draft('另一个新连接')})}catch(error){secondCode=error.details.code}
    return {firstCode,lookupCode,secondCode,pendingCount:client.getPendingIds().length,transportPostAttempts:window.__auditPostAttempts}
  })
  const topbarButtons = await page.locator('.topbar-tools').getByRole('button').count()
  console.log(JSON.stringify({case:'unknown-operation-auto-lookup',...lock,topbarButtons}))
  assert.equal(lock.firstCode,'OPERATION_NOT_FOUND')
  assert.equal(lock.lookupCode,'operation_unknown')
  assert.equal(lock.secondCode,'OPERATION_NOT_FOUND')
  assert.equal(lock.transportPostAttempts,2)
  assert.equal(topbarButtons,0)
  assert.deepEqual(outside,[])
  assert.deepEqual(errors,[])
  console.log(JSON.stringify({case:'isolation',browser:browser.version(),outsideRequests:0,realBusinessRequests:0,note:'正常退出仅表示观测完成，不代表功能通过'}))
} finally {
  await browser?.close()
  if(server.exitCode===null){const exited=once(server,'exit');server.kill('SIGTERM');await Promise.race([exited,delay(5000)]);if(server.exitCode===null){server.kill('SIGKILL');await exited}}
}
