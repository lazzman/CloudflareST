import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {spawn,spawnSync} from 'node:child_process'
import {createServer} from 'node:net'
import {once} from 'node:events'
import {setTimeout as delay} from 'node:timers/promises'
import {mkdir} from 'node:fs/promises'
import {join} from 'node:path'
const root=process.cwd()
const work=process.argv[2]
const require=createRequire(join(root,'internal/management/web/package.json'))
const {chromium}=require('playwright')
const build=spawnSync('go',['build','-o',join(work,'server'),'./cmd/cfst-web'],{cwd:root,encoding:'utf8',env:{...process.env,GOPROXY:'off',GOSUMDB:'off'}})
assert.equal(build.status,0,build.stderr)
const portHolder=createServer().listen(0,'127.0.0.1');await once(portHolder,'listening');const port=portHolder.address().port;await new Promise(resolve=>portHolder.close(resolve))
const origin='http://127.0.0.1:'+port
const password='FAKE-only-local-acceptance-20261115'
const server=spawn(join(work,'server'),['-listen','127.0.0.1:'+port,'-data',join(work,'data')],{env:{...process.env,CFST_ADMIN_PASSWORD:password,CFST_RUNTIME_DIR:join(work,'runtime'),CFST_TRUSTED_PROXIES:'',CFST_SECURE_COOKIE:'false'},stdio:['ignore','pipe','pipe']})
let browser
try{
  for(let attempt=0;attempt<100;attempt++){try{if((await fetch(origin+'/healthz')).ok)break}catch{}await delay(100)}
  browser=await chromium.launch({headless:true,executablePath:'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',args:['--disable-background-networking']})
  const ctx=await browser.newContext({serviceWorkers:'block'})
  await ctx.route('**/*',route=>new URL(route.request().url()).origin===origin?route.continue():route.abort())
  const page=await ctx.newPage();page.setDefaultTimeout(10000)
  await page.goto(origin+'/#/login');await page.getByLabel('管理密码',{exact:true}).fill(password);await page.getByRole('button',{name:'登录管理端',exact:true}).click();await page.waitForURL('**/#/tasks')
  async function fillTask(){
    await page.goto(origin+'/#/tasks/new');await page.getByLabel('任务名称（必填）',{exact:true}).fill('响应丢失后重复确认-验收')
    await page.locator('.source-row select').first().selectOption('ip');await page.getByLabel('来源 1 值',{exact:true}).fill('127.0.0.1')
    await page.getByLabel(/探测方式/).selectOption('tcp');await page.getByRole('checkbox',{name:'启用下载测速与过滤',exact:true}).uncheck()
  }
  const operationIds=[];let dropped=false
  await page.route('**/api/v1/tasks',async route=>{
    if(route.request().method()==='POST'){
      operationIds.push(route.request().postDataJSON().operation_id)
      if(!dropped){dropped=true;const response=await route.fetch();assert.equal(response.status(),202);await route.abort('connectionreset');return}
    }
    await route.continue()
  })
  await fillTask();await page.getByRole('button',{name:'保存完整配置',exact:true}).click()
  await page.getByRole('alert').filter({hasText:'未能确认写入结果'}).waitFor()
  const before=await page.evaluate(async()=>({tasks:(await(await fetch('/api/v1/tasks')).json()).length,pending:JSON.parse(localStorage.getItem('cloudflarest.pending-operation-ids'))}))
  const dialogs=[]
  page.on('dialog',dialog=>{dialogs.push({type:dialog.type(),message:dialog.message()});void dialog.accept()})
  await page.reload();await fillTask();await page.getByRole('button',{name:'保存完整配置',exact:true}).click();await page.waitForURL(url=>url.hash.startsWith('#/tasks/')&&!url.hash.endsWith('/new'))
  const after=await page.evaluate(async()=>({tasks:(await(await fetch('/api/v1/tasks')).json()).length,pending:JSON.parse(localStorage.getItem('cloudflarest.pending-operation-ids'))}))
  console.log(JSON.stringify({case:'C01-response-lost-page-reload',before,after,operationIds,dialogs,duplicateCreated:after.tasks===2&&operationIds.length===2&&operationIds[0]!==operationIds[1]}))
  const timezoneTest=await page.evaluate(async()=>{
    const session=await(await fetch('/api/v1/session')).json()
    const headers={'Content-Type':'application/json','X-CSRF-Token':session.csrf_token}
    let response=await fetch('/api/v1/settings',{method:'PATCH',headers,body:JSON.stringify({operation_id:crypto.randomUUID(),timezone:'UTC',history_days:30})});if(!response.ok)throw new Error('settings '+response.status)
    const defaults=await(await fetch('/api/v1/defaults')).json()
    const task={name:'UTC计划显示验收',config:{...defaults.config,sources:[{kind:'ip',value:'127.0.0.1'}],probe_mode:'tcp',download_enabled:false},schedule:{enabled:true,kind:'cron',expression:'0 0 1 1 *'},dns_auto:false,targets:[],notifications:{telegram:{enabled:false,connection_id:''},wecom:{enabled:false,connection_id:''}}}
    response=await fetch('/api/v1/tasks',{method:'POST',headers,body:JSON.stringify({operation_id:crypto.randomUUID(),task})});const receipt=await response.json();if(!response.ok)throw new Error(JSON.stringify(receipt))
    const saved=await(await fetch('/api/v1/tasks/'+receipt.object_id)).json()
    return {id:saved.id,next:saved.schedule.next_run_at,timezone:'UTC'}
  })
  await page.goto(origin+'/#/tasks/'+timezoneTest.id);await page.getByRole('heading',{name:'UTC计划显示验收',exact:true}).waitFor();await delay(2500)
  const displayed=await page.locator('dl>div').filter({has:page.locator('dt',{hasText:'下次未来时点'})}).locator('dd').innerText()
  const topbar=await page.locator('.global-status').innerText()
  const expected=await page.evaluate(next=>new Intl.DateTimeFormat('zh-CN',{timeZone:'UTC',dateStyle:'short',timeStyle:'medium',hour12:false}).format(new Date(next)),timezoneTest.next)
  console.log(JSON.stringify({case:'timezone-UTC',...timezoneTest,displayed,expected,topbar,mismatch:displayed!==expected}))
  const probeServer=createServer(socket=>socket.end()).listen(0,'127.0.0.1');await once(probeServer,'listening')
  const probePort=probeServer.address().port
  try {
    const localRun=await page.evaluate(async probePort=>{
      const session=await(await fetch('/api/v1/session')).json();const headers={'Content-Type':'application/json','X-CSRF-Token':session.csrf_token}
      const defaults=await(await fetch('/api/v1/defaults')).json()
      const task={name:'真实单IP回环优选验收',config:{...defaults.config,sources:[{kind:'ip',value:'127.0.0.1'}],probe_mode:'tcp',tcp_port:probePort,probe_count:1,ipv4_limit:1,download_enabled:false},schedule:{enabled:false,kind:'interval',interval_minutes:60},dns_auto:false,targets:[],notifications:{telegram:{enabled:false,connection_id:''},wecom:{enabled:false,connection_id:''}}}
      let response=await fetch('/api/v1/tasks',{method:'POST',headers,body:JSON.stringify({operation_id:crypto.randomUUID(),task})});const saved=await response.json();if(!response.ok)throw new Error(JSON.stringify(saved))
      response=await fetch('/api/v1/tasks/'+saved.object_id+'/runs',{method:'POST',headers,body:JSON.stringify({operation_id:crypto.randomUUID()})});const receipt=await response.json();if(!response.ok)throw new Error(JSON.stringify(receipt));return{task:saved.object_id,run:receipt.object_id}
    },probePort)
    let result
    for(let attempt=0;attempt<80;attempt++){
      result=await page.evaluate(async route=>(await(await fetch(route)).json()),'/api/v1/tasks/'+localRun.task+'/runs/'+localRun.run)
      if(!['running','queued'].includes(result.status))break
      await delay(100)
    }
    assert.equal(result.status,'completed');assert.equal(result.result.candidates.length,1);assert.equal(result.result.candidates[0].ip,'127.0.0.1');assert.equal(result.result.candidates[0].rank,1);assert.equal(result.deliveries.length,0)
    console.log(JSON.stringify({case:'real-loopback-single-IP-run',status:result.status,candidates:result.result.candidates.length,eligible:result.result.candidates[0].eligible,ip:result.result.candidates[0].ip,deliveries:result.deliveries.length}))
  } finally {await new Promise(resolve=>probeServer.close(resolve))}
  await page.goto(origin+'/#/tasks/new');await page.getByLabel('任务名称（必填）',{exact:true}).fill('CIDR覆盖验收');await page.locator('.source-row select').first().selectOption('cidr');await page.getByLabel('来源 1 值',{exact:true}).fill('192.0.2.0/28')
  console.log(JSON.stringify({case:'per-CIDR-count-editor',numericInputs:await page.locator('.source-row input[type=number]').count(),sourceText:await page.locator('.source-row').innerText()}))
  if (after.tasks>before.tasks || displayed!==expected || await page.locator('.source-row input[type=number]').count()===0) {
    console.error('边界验收未通过：发现重复提交、时区展示或每CIDR配置入口问题；详见已输出证据。')
    process.exitCode=2
  }
  await mkdir(join(work,'evidence'),{recursive:true});await page.screenshot({path:join(work,'evidence','per-cidr-form.png'),fullPage:true})
}finally{
  await browser?.close();const closed=once(server,'exit');server.kill('SIGTERM');await Promise.race([closed,delay(4000)]);if(server.exitCode===null){server.kill('SIGKILL');await closed}
}
