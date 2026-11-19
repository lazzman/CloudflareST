import assert from 'node:assert/strict'
import {spawn,spawnSync} from 'node:child_process'
import {createRequire} from 'node:module'
import {once} from 'node:events'
import {mkdtemp,readFile,rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createServer} from 'node:net'
import {fileURLToPath} from 'node:url'
import {setTimeout as delay} from 'node:timers/promises'

const root=fileURLToPath(new URL('../',import.meta.url))
const require=createRequire(join(root,'internal/management/web/package.json'))
const {chromium}=require('playwright')
const scratch=await mkdtemp(join(tmpdir(),'cfst-rc-fixed-'))
const binary=join(scratch,'cfst-web')
const password='FAKE_local_RC_browser_2026'
const outside=[]
const writes=[]
const errors=[]
const checks=[]
let server,browser
let output=''
try {
  assert((await readFile(join(root,'internal/management/web/dist/index.html'),'utf8')).includes('/assets/'))
  const built=spawnSync('go',['build','-o',binary,'./cmd/cfst-web'],{cwd:root,encoding:'utf8',env:{...process.env,GOPROXY:'off',GOSUMDB:'off'}})
  assert.equal(built.status,0,built.stderr)
  const listener=createServer().listen(0,'127.0.0.1');await once(listener,'listening')
  const port=listener.address().port;await new Promise(resolve=>listener.close(resolve))
  const origin='http://127.0.0.1:'+port
  server=spawn(binary,['-listen','127.0.0.1:'+port,'-data',join(scratch,'data')],{cwd:root,env:{...process.env,CFST_ADMIN_PASSWORD:password,CFST_SECURE_COOKIE:'false',CFST_TRUSTED_PROXIES:'',CFST_RUNTIME_DIR:join(scratch,'runtime')},stdio:['ignore','pipe','pipe']})
  server.stdout.on('data',chunk=>{output+=chunk.toString()});server.stderr.on('data',chunk=>{output+=chunk.toString()})
  let ready=false
  for(let attempt=0;attempt<100;attempt++){try{if((await fetch(origin+'/healthz')).ok){ready=true;break}}catch{}assert.equal(server.exitCode,null,output);await delay(100)}
  assert(ready,output)
  browser=await chromium.launch({headless:true,args:['--disable-background-networking','--disable-component-update','--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],...(process.env.PLAYWRIGHT_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_EXECUTABLE_PATH}:{})})
  const context=await browser.newContext({viewport:{width:1280,height:900},serviceWorkers:'block'})
  let dropConnection=true
  await context.route('**/*',route=>{
    const request=route.request(),url=new URL(request.url())
    if(url.origin!==origin){outside.push(url.href);return route.abort()}
    assert(!/\/(dns-deliveries|notifications)$/.test(url.pathname),'禁止真实投递请求')
    if(request.method()!=='GET'&&request.method()!=='HEAD'&&url.pathname!=='/api/v1/session')writes.push({path:url.pathname,method:request.method(),body:request.postDataJSON()})
    if(request.method()==='POST'&&url.pathname==='/api/v1/connections'&&dropConnection){dropConnection=false;return route.abort('failed')}
    return route.continue()
  })
  const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message))
  await page.goto(origin+'/#/login');await page.getByLabel('管理密码',{exact:true}).fill(password);await page.getByRole('button',{name:'登录管理端',exact:true}).click()
  await page.getByRole('heading',{name:'优选策略',exact:true}).waitFor()
  await page.goto(origin+'/#/connections');await page.getByRole('heading',{name:'服务连接',exact:true}).waitFor()
  async function createConnection(name) {
    await page.getByRole('button',{name:'新增连接',exact:true}).click()
    const dialog=page.getByRole('dialog',{name:'新增连接'})
    await dialog.locator('select').first().selectOption('telegram')
    await dialog.getByLabel('连接名称',{exact:true}).fill(name)
    await dialog.getByLabel('Bot Token',{exact:true}).fill('100001:FAKE_UNUSED_TOKEN')
    await dialog.getByLabel('Chat ID',{exact:true}).fill('-100001')
    await dialog.getByRole('button',{name:'保存连接',exact:true}).click()
    return dialog
  }
  let dialog=await createConnection('未到达的原连接')
  await dialog.getByText('OPERATION_NOT_FOUND',{exact:true}).waitFor()
  const original=writes.find(item=>item.path==='/api/v1/connections').body.operation_id
  assert.equal(writes.filter(item=>item.path==='/api/v1/connections').length,1,'自动查询不能重交连接')
  await dialog.getByRole('button',{name:'关闭',exact:true}).click()
  assert.equal(await page.locator('.topbar-tools').getByRole('button').count(),0,'顶栏只保留外观控制')
  const read=path=>page.evaluate(async path=>{const response=await fetch(path);return{status:response.status,body:await response.json()}},path)
  const recoveryId=crypto.randomUUID()
  const prepared=await page.evaluate(async ({original,recoveryId})=>{
    const session=await(await fetch('/api/v1/session')).json()
    const response=await fetch('/api/v1/operations/'+original+'/recovery',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf_token},body:JSON.stringify({operation_id:recoveryId,method:'POST',path:'/api/v1/connections',risk_acknowledged:true})})
    return{status:response.status,body:await response.json()}
  },{original,recoveryId})
  assert.equal(prepared.status,202)
  const newId=crypto.randomUUID()
  const created=await page.evaluate(async ({newId,original,recoveryId})=>{
    const session=await(await fetch('/api/v1/session')).json()
    const response=await fetch('/api/v1/connections',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf_token},body:JSON.stringify({operation_id:newId,prior_operation_id:original,recovery_id:recoveryId,connection:{kind:'telegram',name:'恢复许可后的新连接',secrets:{bot_token:'100001:FAKE_UNUSED_TOKEN',chat_id:'-100001'}}})})
    return{status:response.status,body:await response.json()}
  },{newId,original,recoveryId})
  assert.equal(created.status,202)
  const connections=await read('/api/v1/connections');assert.equal(connections.body.length,1)
  const receipt=await read('/api/v1/operations/'+newId)
  assert.equal(receipt.body.prior_operation_id,original)
  const originalReceipt=await read('/api/v1/operations/'+original)
  assert.equal(originalReceipt.body.state,'unknown');assert.equal(originalReceipt.body.recovery.consumed_by,newId)
  const recoveryReceipt=await read('/api/v1/operations/'+recoveryId)
  assert.equal(recoveryReceipt.body.recovery.consumed_by,newId)
  checks.push({case:'RC-04-backend',status:'passed',originalNeverReachedServer:true,automaticResubmits:0,connectionCount:1,linkedOriginal:true,permitConsumed:true})
  await page.goto(origin+'/#/tasks/new');await page.getByRole('heading',{name:'新建策略',exact:true}).waitFor()
  await page.getByLabel('策略名称（必填）',{exact:true}).fill('敏感URL拒绝')
  await page.locator('#editor-step-advanced').click()
  await page.locator('#advanced-tab-download').click()
  await page.getByLabel('自定义测速文件 URL（HTTP／HTTPS）',{exact:true}).fill('https://example.invalid/file?token=FAKE_HIDDEN_DOWNLOAD_VALUE')
  const beforeTasks=writes.filter(item=>item.path==='/api/v1/tasks').length
  await page.getByRole('button',{name:'保存策略',exact:true}).click()
  await page.getByRole('alert').getByText('配置未保存，请修正以下内容',{exact:true}).waitFor()
  assert.equal(writes.filter(item=>item.path==='/api/v1/tasks').length,beforeTasks)
  page.on('dialog',dialog=>dialog.accept())
  const createdFailure=await page.evaluate(async()=>{
    const session=await(await fetch('/api/v1/session')).json()
    const defaults=await(await fetch('/api/v1/defaults')).json()
    const config={...defaults.config,probe_mode:'tcp',ipv4_enabled:true,ipv6_enabled:false,download_enabled:false,filter_order:['latency','loss'],sources:[{kind:'ip',value:'2001:db8::1'}],download_url:'http://127.0.0.1/never-download?bytes=100'}
    const post=async(path,body)=>{const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf_token},body:JSON.stringify({...body,operation_id:crypto.randomUUID()})});assertResponse(response);return response.json()}
    function assertResponse(response){if(!response.ok)throw new Error('本地夹具写入失败 '+response.status)}
    const saved=await post('/api/v1/tasks',{task:{name:'实际失败原因测试',config,schedule:{enabled:false,kind:'interval',interval_minutes:60},dns_auto:false,targets:[],notifications:{}}})
    const run=await post('/api/v1/tasks/'+saved.object_id+'/runs',{})
    return {task:saved.object_id,run:run.object_id}
  })
  let failure
  for(let attempt=0;attempt<80;attempt++){failure=(await read('/api/v1/tasks/'+createdFailure.task+'/runs/'+createdFailure.run+'?view=summary')).body;if(failure.status==='failed')break;await delay(100)}
  assert.equal(failure.status,'failed');assert.equal(failure.candidate_summary.total,0);assert.equal(failure.deliveries.length,0)
  await page.goto(origin+'/#/tasks/'+createdFailure.task+'/runs/'+createdFailure.run)
  await page.getByRole('heading',{name:'运行原因与阶段错误',exact:true}).waitFor()
  assert((await page.locator('[aria-label="运行结束与优选阶段原因"]').innerText()).includes(failure.result.error))
  for(const width of [1280,320]) {await page.setViewportSize({width,height:900});for(const colorScheme of ['light','dark']){await page.emulateMedia({colorScheme});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))}}
  checks.push({case:'RC-01/RC-05',status:'passed',sensitiveURLRejectedBeforePost:true,realFailedRunReasonVisible:true,networkProbes:0,deliveries:0,narrowLayout:true})
  assert.deepEqual(outside,[]);assert.deepEqual(errors,[])
  console.log(checks.map(item=>JSON.stringify(item)).join('\n'))
} catch(error){console.error(output);throw error}
finally {
  await browser?.close()
  if(server&&server.exitCode===null){const closed=once(server,'exit');server.kill('SIGTERM');await Promise.race([closed,delay(5000)]);if(server.exitCode===null){server.kill('SIGKILL');await closed}}
  await rm(scratch,{recursive:true,force:true})
}
