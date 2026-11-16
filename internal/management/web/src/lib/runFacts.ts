import type { Candidate, OptimizerConfig, Run } from '../api/types'
import { metric } from './domain'

export function downloadFacts(candidate: Candidate, config?: Partial<OptimizerConfig>) {
  if (!candidate.download_end && candidate.speed_mb_s == null && !candidate.download_bytes && !candidate.download_seconds) return '下载未测试；未产生字节数、时长及速度样本'
  const ending = candidate.download_end === 'completed'
    ? typeof candidate.download_seconds === 'number' && typeof config?.download_window_seconds === 'number' && candidate.download_seconds < config.download_window_seconds ? '提前完整下载' : '文件完整下载'
    : candidate.download_end === 'window' ? '正文窗口到期（不是文件完整下载）'
    : candidate.download_end === 'failed' ? '下载失败，部分数据不作为有效速度样本'
    : candidate.download_end === 'cancelled' ? '下载已取消，未形成有效样本' : '下载结束方式未知'
  const bytes = typeof candidate.download_bytes === 'number' ? candidate.download_bytes.toLocaleString('zh-CN') + ' 字节' : '字节数未知'
  return ending + ' · ' + bytes + ' / ' + metric(candidate.download_seconds, ' s')
}

export function elapsedSince(value?: string, now = Date.now()) {
  const start = Date.parse(value || '')
  if (!Number.isFinite(start) || !Number.isFinite(now) || now < start) return '未知'
  const seconds = Math.floor((now - start) / 1000)
  return Math.floor(seconds / 60) + ' 分 ' + seconds % 60 + ' 秒'
}

export function bestCandidates(run?: Run) {
  return run?.candidate_summary?.families.flatMap(item => item.best ? [item.best] : []) || (run?.result?.candidates || []).filter(item => item.eligible && item.rank === 1)
}
