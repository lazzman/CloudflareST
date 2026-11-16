import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createRequire} from 'node:module'
import {fileURLToPath} from 'node:url'
import {join} from 'node:path'
import {createServer} from 'node:net'
import {once} from 'node:events'
import {setTimeout as delay} from 'node:timers/promises'
const root=fileURLToPath(new URL('../../',import.meta.url))
const web=join(root,'internal/management/web')
const require=createRequire(join(web,'package.json'))
const {chromium}=require('playwright')
const listener=createServer().listen(0,'127.0.0.1');await once(listener,'listening')
const port=listener.address().port;await new Promise(resolve=>listener.close(resolve))
const origin='http://127.0.0.1:'+port
const server=spawn(process.execPath,['node_modules/vite/bin/vite.js','--mode','mock','--host','127.0.0.1','--port',String(port),'--strictPort'],{cwd:web,stdio:['ignore','pipe','pipe']})
let diagnostics='';server.stdout.on('data',data=>{diagnostics+=data});server.stderr.on('data',data=>{diagnostics+=data})
let browser
const outside=[],errors=[]
try{
  let ready=false
  for(let attempt=0;attempt<80;attempt++){try{if((await fetch(origin)).ok){ready=true;break}}catch{}await delay(100)}
  assert(ready,diagnostics)
  browser=await chromium.launch({headless:true,args:['--disable-background-networking','--disable-component-update','--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],...(process.env.PLAYWRIGHT_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_EXECUTABLE_PATH}:{})})
  const context=await browser.newContext({viewport:{width:1280,height:900},serviceWorkers:'block'})
  await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin){outside.push(route.request().url());return route.abort()}return route.continue()})
  const page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message))
  await page.goto(origin+'/#/login');await page.getByLabel('管理密码').fill('仅隔离核验');await page.getByRole('button',{name:'登录管理端'}).click();await page.getByRole('heading',{name:'优选任务',exact:true}).waitFor()
  await page.evaluate(async()=>{
    const client=await import('/src/api/client.ts'),mock=await import('/src/api/mock.ts')
    window.__auditTaskWrites=[]
    client.setTransport(async(input,init={})=>{if((init.method||'GET')!=='GET')window.__auditTaskWrites.push({path:String(input),method:init.method});return mock.mockFetch(input,init)})
  })
  await page.goto(origin+'/#/tasks/new');await page.getByRole('heading',{name:'新建任务',exact:true}).waitFor()
  await page.getByLabel('任务名称（必填）',{exact:true}).fill('有限正延迟阈值核验')
  await page.locator('#latency_max_ms').fill('60001')
  await page.getByRole('button',{name:'保存完整配置',exact:true}).click()
  await page.getByRole('alert').getByText('配置未保存，请修正以下内容',{exact:true}).waitFor()
  const reason=await page.getByRole('alert').innerText()
  const writes=await page.evaluate(()=>window.__auditTaskWrites)
  const implementation=await page.evaluate(async()=>{
    const {DEFAULT_CONFIG,validateTask}=await import('/src/lib/domain.ts')
    return validateTask({id:'',name:'边界核验',config:{...DEFAULT_CONFIG,latency_max_ms:60001},schedule:{enabled:false,kind:'interval',interval_minutes:60}})
  })
  console.log(JSON.stringify({case:'latency-upper-bound',input:60001,uiBlocksSave:reason.includes('60000'),clientError:implementation.latency_max_ms,businessWrites:writes.length,expected:'PRD只要求有限且大于0；后端允许60001'}))
  assert.equal(writes.length,0);assert.deepEqual(outside,[]);assert.deepEqual(errors,[])
  console.log(JSON.stringify({case:'isolation',browser:browser.version(),externalRequests:0,realBusinessRequests:0,note:'正常退出表示观测完成，非本项验收通过'}))
}finally{await browser?.close();if(server.exitCode===null){const stopped=once(server,'exit');server.kill('SIGTERM');await Promise.race([stopped,delay(5000)]);if(server.exitCode===null){server.kill('SIGKILL');await stopped}}}
