import { describe, expect, it } from 'vitest'
import { bestCandidates, downloadFacts, elapsedSince } from '../src/lib/runFacts'
import { familyResult, qualifiedCount } from '../src/lib/domain'
import type { Run } from '../src/api/types'

describe('完整结果与分页摘要', () => {
  it('未执行下载不显示零样本，提前完成和窗口结束分别显示', () => {
    expect(downloadFacts({ip:'192.0.2.1', download_bytes:0, download_seconds:0, speed_mb_s:null})).toContain('未测试')
    expect(downloadFacts({ip:'192.0.2.1', download_bytes:30000000, download_seconds:3, download_end:'completed'}, {download_window_seconds:10})).toContain('提前完整下载')
    expect(downloadFacts({ip:'192.0.2.1', download_bytes:30000000, download_seconds:10, download_end:'window'})).toContain('窗口到期')
    expect(downloadFacts({ip:'192.0.2.1', download_bytes:100, download_seconds:1, download_end:'failed'})).toContain('不作为有效速度样本')
  })
  it('元数据摘要不需要整组候选也能给出两族合格数和第一名', () => {
    const run: Run = {id:'run', task_id:'task', status:'completed', result:{status:'completed'}, candidate_summary:{total:100000, families:[{family:'ipv4', count:100000, eligible:20, best:{ip:'192.0.2.1', eligible:true, rank:1}}, {family:'ipv6', count:0, eligible:0}]}}
    expect(qualifiedCount(run,'ipv4')).toBe(20)
    expect(familyResult(run,'ipv4').best?.ip).toBe('192.0.2.1')
    expect(bestCandidates(run)).toHaveLength(1)
  })
  it('等待时间使用提供的服务端时刻，未知时刻不冒充零', () => {
    expect(elapsedSince('2026-11-15T00:00:00Z', Date.parse('2026-11-15T00:02:03Z'))).toBe('2 分 3 秒')
    expect(elapsedSince(undefined)).toBe('未知')
    expect(elapsedSince('2026-11-15T00:00:00Z', 0)).toBe('未知')
  })
})
