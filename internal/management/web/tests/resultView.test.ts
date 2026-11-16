import { describe, expect, it } from 'vitest'
import { compareResults, automaticActions, type ResultSort } from '../src/lib/resultView'
import type { Candidate } from '../src/api/types'
describe('结果阅读排序', () => {
  it.each(['latency_ms','speed_mb_s','loss_percent'] as ResultSort[])('%s升降序都将缺失值放最后且不改变原数组', field => {
    const rows: Candidate[] = [{ip:'a',[field]:null},{ip:'b',[field]:20},{ip:'c',[field]:2},{ip:'d'}]
    expect([...rows].sort((first,second)=>compareResults(first,second,field,'asc')).map(row=>row.ip)).toEqual(['c','b','a','d'])
    expect([...rows].sort((first,second)=>compareResults(first,second,field,'desc')).map(row=>row.ip)).toEqual(['b','c','a','d'])
    expect(rows.map(row=>row.ip)).toEqual(['a','b','c','d'])
  })
  it('延迟相同时速度高的在前，缺失速度不伪造0', () => {
    const rows: Candidate[] = [{ip:'a',latency_ms:10,speed_mb_s:null},{ip:'b',latency_ms:10,speed_mb_s:5},{ip:'c',latency_ms:10,speed_mb_s:20}]
    expect([...rows].sort((first,second)=>compareResults(first,second,'latency_ms','asc')).map(row=>row.ip)).toEqual(['c','b','a'])
  })
  it('高级功能收起不掩盖已开启的自动动作', () => {
    expect(automaticActions({id:'task',dns_auto:true,notifications:{telegram:{enabled:true},wecom:{enabled:false}}})).toBe('DNS、Telegram')
    expect(automaticActions({id:'task'})).toBe('')
  })
})
