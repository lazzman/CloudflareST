import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'

const saved = new Map<string,string>()
const json = (value:unknown,status=200) => new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}})
beforeEach(()=>{vi.resetModules();saved.clear();vi.stubGlobal('localStorage',{getItem:(key:string)=>saved.get(key)||null,setItem:(key:string,value:string)=>saved.set(key,value)})})
afterEach(()=>{vi.unstubAllGlobals();vi.restoreAllMocks()})

async function setup() {
  const client = await import('../src/api/client')
  let review:any
  let newID=''
  let failRecovery=false
  const writes:{path:string;body:any;headers:Headers}[]=[]
  const transport=vi.fn(async(input:RequestInfo|URL,options:RequestInit={})=>{
    const path=String(input)
    if ((options.method||'GET')==='GET') {
      if(review && path.endsWith('/'+review.id)) return json({operation_id:review.id,state:'completed',result:review,recovery:review})
      if(review && path.endsWith('/'+review.prior_operation_id)) return json({operation_id:review.prior_operation_id,state:'unknown',recovery_operation_id:review.id,recovery:review})
      return json({code:'operation_unknown',message:'查不到原号，不代表未执行'},404)
    }
    const body=JSON.parse(String(options.body||'{}'));const headers=new Headers(options.headers)
    writes.push({path,body,headers})
    if(path.endsWith('/recovery')) {
      review={id:body.operation_id,prior_operation_id:path.split('/')[4],path:body.path,method:body.method}
      if(failRecovery)throw new Error('模拟核查提交后响应丢失')
      return json({operation_id:review.id,state:'completed',result:review,recovery:review},202)
    }
    if(!body.recovery_id) throw new Error('模拟最初连接创建结果未知')
    expect(body.prior_operation_id).toBe(review.prior_operation_id)
    expect(body.recovery_id).toBe(review.id)
    newID=body.operation_id;review.consumed_by=newID
    return json({operation_id:newID,state:'completed',prior_operation_id:body.prior_operation_id,recovery_id:body.recovery_id},202)
  })
  client.setTransport(transport)
  await expect(client.write('/connections',{body:{connection:{name:'原连接',secrets:{bot_token:'FAKE_TOKEN_NOT_REAL'}}}})).rejects.toMatchObject({details:{code:'NETWORK_ERROR'}})
  const original=client.getPendingIds()[0]!
  return {client,transport,writes,original,failReview:()=>{failRecovery=true},review:()=>review,newID:()=>newID}
}

describe('人工核查后受控新尝试',()=>{
  it.each(['unconfirmed','expired'])('旧版本号记录从%s服务端收据恢复非创建动作范围',async state=>{
    const client=await import('../src/api/client')
    const id=crypto.randomUUID()
    saved.set(client.OPERATION_STORAGE_KEY,JSON.stringify([id]));client.restoreOperationIds()
    const action='POST /api/v1/runs/old-run/notifications'
    client.setTransport(async()=>state==='expired'?json({code:'operation_expired',message:'收据过期',operation_id:id,action},410):json({operation_id:id,state,action}))
    try{await client.lookupOperation(id)}catch{}
    expect(client.getOperationScope(id)).toEqual({method:'POST',path:'/api/v1/runs/old-run/notifications'})
    expect(client.getPendingIds()).toContain(id)
    expect(client.getUnscopedIds()).toHaveLength(0)
    await expect(client.write('/runs/old-run/notifications',{body:{channels:[]}})).rejects.toMatchObject({details:{code:'ACTION_UNCONFIRMED'}})
  })
  it('未知查询不解锁，明确风险核查只领取许可，不自动重发',async()=>{
    const test=await setup()
    await expect(test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},false)).rejects.toMatchObject({details:{code:'RECOVERY_REVIEW_REQUIRED'}})
    await expect(test.client.lookupOperation(test.original)).rejects.toMatchObject({status:404})
    await expect(test.client.write('/connections',{body:{connection:{name:'另一连接'}}})).rejects.toMatchObject({details:{code:'ACTION_UNCONFIRMED'}})
    expect(test.writes).toHaveLength(1)
    await test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},true)
    expect(test.writes.map(item=>item.path)).toEqual(['/api/v1/connections','/api/v1/operations/'+test.original+'/recovery'])
    expect(test.client.getPendingIds()).toHaveLength(0)
    expect(test.client.getRecoveryPermits()).toHaveLength(1)
    expect([...saved.values()].join('')).not.toContain('FAKE_TOKEN_NOT_REAL')
    vi.resetModules()
    const restored=await import('../src/api/client');restored.restoreOperationIds();restored.setTransport(test.transport)
    await restored.write('/connections',{body:{connection:{name:'经核查的新连接',secrets:{bot_token:'FAKE_REENTERED'}}}})
    expect(test.writes).toHaveLength(3)
    expect(restored.getRecoveryPermits()).toHaveLength(0)
    await restored.lookupOperation(test.review().id)
    expect(restored.getRecoveryPermits()).toHaveLength(0)
    expect(test.review().consumed_by).toBe(test.newID())
  })
  it('核查响应丢失后，读取原号找回许可而非再次申请',async()=>{
    const test=await setup();test.failReview()
    await expect(test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},true)).rejects.toMatchObject({uncertain:true})
    expect(test.client.getPendingIds()).toHaveLength(2)
    await test.client.lookupOperation(test.original)
    expect(test.writes).toHaveLength(2)
    expect(test.client.getPendingIds()).toHaveLength(0)
    expect(test.client.getRecoveryPermits()[0]?.prior_operation_id).toBe(test.original)
  })
  it('核查许可落本机失败时保留原锁和核查号，可只读恢复',async()=>{
    const test=await setup()
    const original=test.transport.getMockImplementation()!
    test.client.setTransport(async(input,options)=>{
      const response=await original(input,options)
      if(String(input).endsWith('/recovery'))vi.spyOn(localStorage,'setItem').mockImplementation(()=>{throw new Error('空间不足')})
      return response
    })
    await expect(test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},true)).rejects.toMatchObject({details:{code:'OPERATION_STORAGE_FAILED'},uncertain:true})
    expect(test.client.getPendingIds()).toContain(test.original)
    expect(test.client.getRecoveryPermits()).toHaveLength(0)
    vi.restoreAllMocks();test.client.setTransport(test.transport)
    await test.client.lookupOperation(test.original)
    expect(test.client.getRecoveryPermits()).toHaveLength(1)
  })
  it('核查请求未到达时再次明确确认仅复用原核查号，不换号申请',async()=>{
    const test=await setup()
    let missed=''
    let fail=true
    test.client.setTransport(async(input,options)=>{
      if(String(input).endsWith('/recovery')&&fail){fail=false;missed=JSON.parse(String(options?.body)).operation_id;throw new Error('模拟未抵达服务端')}
      return test.transport(input,options)
    })
    await expect(test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},true)).rejects.toMatchObject({uncertain:true})
    await test.client.prepareOperationRecovery(test.original,{method:'POST',path:'/api/v1/connections'},true)
    expect(test.review().id).toBe(missed)
    expect(test.writes).toHaveLength(2)
    expect(test.client.getPendingIds()).toHaveLength(0)
  })
})
