import type { Candidate, Task } from '../api/types'
export type ResultSort = 'latency_ms' | 'speed_mb_s' | 'loss_percent'
export type ResultDirection = 'asc' | 'desc'
export const resultColumns: { key: ResultSort; label: string; unit: string }[] = [
  { key: 'latency_ms', label: '延迟', unit: ' ms' },
  { key: 'speed_mb_s', label: '下载速度', unit: ' MB/s' },
  { key: 'loss_percent', label: '失败比例', unit: '%' },
]
export function compareResults(first: Candidate, second: Candidate, field: ResultSort, direction: ResultDirection) {
  const compare = (key: ResultSort, descending: boolean) => {
    const left = first[key], right = second[key]
    const missingLeft = typeof left !== 'number' || !Number.isFinite(left)
    const missingRight = typeof right !== 'number' || !Number.isFinite(right)
    if (missingLeft || missingRight) return Number(missingLeft) - Number(missingRight)
    return descending ? right! - left! : left! - right!
  }
  return compare(field, direction === 'desc') || (field !== 'latency_ms' ? compare('latency_ms', false) : 0) || (field !== 'speed_mb_s' ? compare('speed_mb_s', true) : 0)
}
export function automaticActions(task?: Task) {
  return [task?.dns_auto ? 'DNS' : '', task?.notifications?.telegram?.enabled ? 'Telegram' : '', task?.notifications?.wecom?.enabled ? '企业微信' : ''].filter(Boolean).join('、')
}
