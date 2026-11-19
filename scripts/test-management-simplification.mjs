import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {createRequire} from 'node:module'
import {once} from 'node:events'
import {mkdir} from 'node:fs/promises'
import {createServer} from 'node:net'
import {join} from 'node:path'
import {fileURLToPath} from 'node:url'
import {setTimeout as delay} from 'node:timers/promises'
const root=fileURLToPath(new URL('../',import.meta.url)),web=join(root,'internal/management/web')
const require=createRequire(join(web,'package.json')),{chromium}=require('playwright')
const output=process.env.SIMPLIFY_OUTPUT_DIR||join(web,'test-results/simplify')
const listener=createServer().listen(0,'127.0.0.1');await once(listener,'listening');const port=listener.address().port;await new Promise(resolve=>listener.close(resolve))
const origin='http://127.0.0.1:'+port,server=spawn(process.execPath,['node_modules/vite/bin/vite.js','--mode','mock','--host','127.0.0.1','--port',String(port),'--strictPort'],{cwd:web,stdio:['ignore','pipe','pipe']})
let diagnostics='',browser,page
const errors=[],outside=[]
server.stdout.on('data',value=>diagnostics+=value);server.stderr.on('data',value=>diagnostics+=value)
try{
 await mkdir(output,{recursive:true});let ready=false
 for(let attempt=0;attempt<100;attempt++){try{if((await fetch(origin)).ok){ready=true;break}}catch{}assert.equal(server.exitCode,null,diagnostics);await delay(100)}assert(ready,diagnostics)
 browser=await chromium.launch({headless:true,args:['--disable-background-networking','--disable-component-update','--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1'],...(process.env.PLAYWRIGHT_EXECUTABLE_PATH?{executablePath:process.env.PLAYWRIGHT_EXECUTABLE_PATH}:{})})
 const context=await browser.newContext({viewport:{width:1440,height:1000},colorScheme:'light',reducedMotion:'reduce',serviceWorkers:'block'})
 await context.route('**/*',route=>{if(new URL(route.request().url()).origin!==origin){outside.push(route.request().url());return route.abort()}return route.continue()})
 page=await context.newPage();page.setDefaultTimeout(10000);page.on('pageerror',error=>errors.push(error.message));page.on('dialog',dialog=>dialog.accept())
 const idle=()=>page.waitForFunction(()=>![...document.querySelectorAll('[data-resource-loading]')].length)
 const open=async(path,title)=>{await page.goto(origin+'/#'+path);await page.waitForFunction(title=>document.title===title+' · CloudflareST',title);await idle()}
 const shot=async name=>{await page.evaluate(()=>document.getElementById('main-content')?.scrollTo({top:0,behavior:'instant'}));await page.evaluate(()=>{if(document.activeElement?.tagName==='H1')document.activeElement.blur()});await page.screenshot({path:join(output,name+'.jpg'),type:'jpeg',quality:85,fullPage:page.viewportSize().width<=700})}
 await page.goto(origin+'/#/login');await page.getByLabel('管理密码',{exact:true}).fill('隔离简化验收');await page.getByRole('button',{name:'登录管理端',exact:true}).click();await page.getByRole('heading',{name:'优选策略',exact:true}).waitFor();await idle()
 await page.evaluate(async()=>{const client=await import('/src/api/client.ts'),mock=await import('/src/api/mock.ts');window.__simpleRequests=[];client.setTransport(async(input,init={})=>{window.__simpleRequests.push({path:String(input),method:init.method||'GET',body:init.body?JSON.parse(String(init.body)):undefined});return mock.mockFetch(input,init)})})
 const requests=()=>page.evaluate(()=>window.__simpleRequests)
 const writes=async()=> (await requests()).filter(item=>item.method!=='GET')
 const task=()=>page.evaluate(async()=>{const {request}=await import('/src/api/client.ts');return request('/tasks/task-demo')})
 const original=await task()
 assert.equal(await page.locator('.overview-grid').count(),0)
 assert.equal(await page.getByRole('navigation',{name:'主导航',exact:true}).getByRole('link').count(),5,'导航应直接展示全部入口')
 assert.equal(await page.getByRole('heading',{name:'我的策略',exact:true}).count(),1)
 await shot('strategies-desktop')
 const row=page.locator('tr').filter({has:page.getByRole('link',{name:'边缘节点 · 每日复核',exact:true})})
 await row.getByRole('switch',{name:'关闭定时：边缘节点 · 每日复核',exact:true}).click();await idle()
 assert.equal((await task()).schedule.enabled,false)
 assert.deepEqual((await task()).notifications,original.notifications);assert.equal((await task()).dns_auto,original.dns_auto)
 await row.getByRole('switch',{name:'开启定时：边缘节点 · 每日复核',exact:true}).click();await idle();assert.equal((await task()).schedule.enabled,true)
 assert((await writes()).every(item=>item.path.endsWith('/schedule-state')))
 await row.getByRole('link',{name:'查看结果',exact:true}).click();await page.waitForURL('**/#/tasks/task-demo/results');await idle()
 assert.equal(await page.locator('.result-table tbody tr').count(),3)
 assert.equal(await page.getByText('当前配置快照',{exact:true}).count(),0)
 assert.equal(await page.locator('.results-advanced').getAttribute('open'),null)
 const beforeSort=(await writes()).length
 await page.getByRole('button',{name:'下载速度',exact:false}).click();await idle()
 assert.equal(await page.locator('th[aria-sort="descending"] .sort-button').textContent(),'下载速度 ↓')
 await page.getByRole('button',{name:'下载速度',exact:false}).click();await idle()
 assert((await page.locator('.result-table tbody tr').first().innerText()).includes('2001:db8::18'))
 await page.getByRole('button',{name:'延迟',exact:false}).click();await idle()
 assert((await page.locator('.result-table tbody tr').first().innerText()).includes('192.0.2.18'))
 const sortedRequests=(await requests()).filter(item=>item.path.includes('/candidates'))
 assert(sortedRequests.some(item=>item.path.includes('sort=speed_mb_s')&&item.path.includes('direction=asc')))
 assert.equal((await writes()).length,beforeSort)
 await shot('results-desktop')
 await page.getByLabel('运行时间',{exact:true}).selectOption('run-demo-001');await idle()
 assert.equal(new URL(page.url()).hash,'#/tasks/task-demo/results?run=run-demo-001')
 await page.getByLabel('结果范围',{exact:true}).selectOption('');await idle();assert.equal(await page.locator('.result-table tbody tr').count(),4)
 await page.locator('.results-advanced > summary').click();assert(await page.getByRole('link',{name:'DNS／通知、详细诊断',exact:true}).isVisible())
 await open('/tasks/task-demo/edit','编辑策略')
 assert(!(await page.locator('.editor-advanced').isVisible()))
 assert(!(await page.locator('#probe_count').isVisible()));assert(!(await page.locator('#download_url').isVisible()))
 await page.locator('#editor-step-measure').click();await page.locator('#latency_max_ms').fill('250')
 await page.getByRole('button',{name:'保存策略',exact:true}).click();await page.waitForURL('**/#/tasks/task-demo/results');await idle()
 const saved=await task(),expectedConfig={...original.config,latency_max_ms:250}
 assert.deepEqual(saved.config,expectedConfig);assert.deepEqual(saved.targets,original.targets);assert.deepEqual(saved.notifications,original.notifications);assert.equal(saved.dns_auto,original.dns_auto);for(const key of Object.keys(original.schedule))assert.deepEqual(saved.schedule[key],original.schedule[key])
 await open('/tasks/task-demo/edit','编辑策略')
 await page.locator('#editor-step-advanced').click();await page.locator('#probe_count').fill('0');await page.locator('#editor-step-basic').click()
 await page.getByRole('button',{name:'保存策略',exact:true}).click();await page.getByRole('heading',{name:'配置未保存，请修正以下内容',exact:true}).waitFor()
 assert(await page.locator('.editor-advanced').isVisible())
 await page.getByRole('link',{name:'请输入 1～100 的整数',exact:true}).focus();await page.keyboard.press('Enter');assert.equal(await page.evaluate(()=>document.activeElement.id),'probe_count')
 await page.locator('#probe_count').fill(String(original.config.probe_count))
 await open('/tasks/new','新建策略');await shot('editor-desktop')
 await page.getByLabel('策略名称（必填）',{exact:true}).fill('简化验收策略')
 await page.getByRole('button',{name:'保存策略',exact:true}).click();await page.waitForURL(/#\/tasks\/[^/]+\/results$/);await idle();await page.getByRole('heading',{name:'还没有运行结果',exact:true}).waitFor()
 const createdId=new URL(page.url()).hash.split('/')[2]
 await open('/tasks','优选策略')
 const createdRow=page.locator('tr').filter({has:page.getByRole('link',{name:'简化验收策略',exact:true})})
 await createdRow.getByRole('button',{name:'运行一次',exact:true}).click();await page.waitForURL(url=>url.hash.startsWith('#/tasks/'+createdId+'/results?run='));await idle()
 assert(await page.getByText('等待运行，完成后将在这里显示 IP 结果。',{exact:true}).isVisible())
 await page.getByRole('button',{name:'停止本次',exact:true}).click();await page.getByRole('button',{name:'确认停止',exact:true}).click();await idle()
 const runId=new URL(page.url()).hash.split('run=')[1]
 assert.equal((await page.evaluate(async({taskId,runId})=>{const {request}=await import('/src/api/client.ts');return request('/tasks/'+taskId+'/runs/'+runId+'?view=summary')},{taskId:createdId,runId})).status,'cancelled')
 for(const width of [1440,390,320]){
  await page.setViewportSize({width,height:900})
  for(const colorScheme of ['light','dark']){
   await page.emulateMedia({colorScheme})
   for(const [path,title,name] of [['/tasks','优选策略','strategies'],['/tasks/task-demo/results','IP 结果','results'],['/tasks/new','新建策略','editor']]){
    await open(path,title)
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),name+'整页溢出')
    if(name==='results'){
     await page.getByRole('button',{name:'下载速度',exact:false}).click();await idle()
     assert.equal(await page.locator('th[aria-sort="descending"] .sort-button').textContent(),'下载速度 ↓')
     assert((await page.locator('.result-table tbody tr').first().innerText()).includes('192.0.2.18'))
     await page.getByRole('button',{name:'延迟',exact:false}).click();await idle()
    }
    if(width===390&&colorScheme==='light')await shot(name+'-mobile')
    if(width===1440&&name==='results'&&colorScheme==='dark')await shot('results-dark')
   }
  }
 }
 assert(!(await writes()).some(item=>item.path.endsWith('/dns-deliveries')||item.path.endsWith('/notifications')))
 await page.evaluate(async()=>{
  const client=await import('/src/api/client.ts'),mock=await import('/src/api/mock.ts'),{router}=await import('/src/router.ts')
  const original=await client.request('/tasks/task-demo/runs/run-demo-001')
  const history=Array.from({length:21},(_,index)=>({...structuredClone(original),id:'history-'+index,queued_at:new Date(Date.now()-index*3600000).toISOString(),status:index===0?'failed':'completed',reason:index===0?'本机测试：最新一次失败，不回退旧成功结果':'',result:index===0?{status:'failed',candidates:[]}:structuredClone(original.result)}))
  window.__simpleHistory={history,reads:[],slowNext:false}
  client.setTransport(async(input,init={})=>{
   const url=new URL(String(input),location.origin),scenario=window.__simpleHistory
   const prefix='/api/v1/tasks/task-demo/runs'
   if((init.method||'GET')==='GET'&&url.pathname.startsWith(prefix)){
    scenario.reads.push(url.pathname+url.search)
    const respond=value=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}})
    if(url.pathname===prefix){
     const page=Number(url.searchParams.get('page')||1),items=scenario.history.slice((page-1)*20,page*20)
     const snapshot=structuredClone({items,total:scenario.history.length,page,page_size:20})
     if(scenario.slowNext&&page===1){scenario.slowNext=false;await new Promise(resolve=>setTimeout(resolve,1200))}
     return respond(snapshot)
    }
    const id=url.pathname.slice(prefix.length+1).split('/')[0],run=scenario.history.find(item=>item.id===id)
    if(run)return respond(url.pathname.endsWith('/candidates')?{items:run.result.candidates.filter(item=>item.eligible),total:run.result.candidates.filter(item=>item.eligible).length,page:1,page_size:50}:run)
   }
   return mock.mockFetch(input,init)
  })
  await router.push('/tasks/task-demo/results')
 })
 await page.getByText('本机测试：最新一次失败，不回退旧成功结果',{exact:true}).waitFor();await idle()
 assert.equal(await page.locator('.result-table').count(),0)
 await page.getByLabel('运行时间',{exact:true}).selectOption('history-1');await idle()
 assert.equal(await page.locator('.result-table tbody tr').count(),3)
 await delay(2200);assert.equal(await page.getByLabel('运行时间',{exact:true}).inputValue(),'history-1')
 await page.getByRole('button',{name:'更早记录',exact:true}).click();await idle()
 await page.getByLabel('运行时间',{exact:true}).selectOption('history-20');await idle()
 assert.equal(await page.locator('.result-table tbody tr').count(),3)
 await page.getByLabel('运行时间',{exact:true}).selectOption('');await idle()
 await page.getByText('本机测试：最新一次失败，不回退旧成功结果',{exact:true}).waitFor()
 assert.equal(await page.getByRole('button',{name:'较新记录',exact:true}).isDisabled(),true)
 await page.evaluate(()=>{window.__simpleHistory.slowNext=true;window.__simpleHistory.reads=[]})
 await page.waitForFunction(()=>!window.__simpleHistory.slowNext)
 await page.getByRole('button',{name:'更早记录',exact:true}).click();await idle();await delay(1400)
 assert.equal(await page.getByRole('button',{name:'更早记录',exact:true}).isDisabled(),true)
 assert.equal(await page.getByRole('button',{name:'较新记录',exact:true}).isEnabled(),true)
 assert.equal(await page.getByLabel('运行时间',{exact:true}).inputValue(),'')
 assert.equal(await page.locator('.result-table').count(),0)
 assert((await page.evaluate(()=>window.__simpleHistory.reads)).some(path=>path.includes('page=2')))
 assert.deepEqual(errors,[]);assert.deepEqual(outside,[])
 console.log(JSON.stringify({status:'passed',oneClickResult:true,globalSortRequest:true,readOnlySorting:true,advancedPreserved:true,hiddenValidationFocus:true,scheduleToggleIsolated:true,runOnceAndCancel:true,latestFailureNotReplaced:true,historyPagination:true,historySelectionSurvivesPoll:true,staleHistoryResponseIgnored:true,mobileSortClickable:true,widths:[1440,390,320],themes:['light','dark'],layoutChecks:18,pageErrors:0,externalRequests:0,screenshots:output,note:'本机替身，无真实测速/DNS/通知'}))
}catch(error){await page?.screenshot({path:join(output,'failure.jpg'),type:'jpeg',quality:85}).catch(()=>{});console.error('简化流程验收未完成：',error,diagnostics);throw error}
finally{await browser?.close();if(server.exitCode===null){const stopped=once(server,'exit');server.kill('SIGTERM');await Promise.race([stopped,delay(5000)]);if(server.exitCode===null){server.kill('SIGKILL');await stopped}}}
