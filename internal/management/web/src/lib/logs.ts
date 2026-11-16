import type { OperationLog } from '../api/types'
import { timeQuery } from './serialize'

export interface LogFilters { action: string; object_id: string; result: string; start: string; end: string }
export function logQuery(filters: LogFilters, timezone: string | undefined) {
  return timeQuery({ action: filters.action.trim(), object: filters.object_id.trim(), result: filters.result, start: filters.start, end: filters.end }, timezone)
}
export function logMatches(log: OperationLog, filters: URLSearchParams) {
  const action = filters.get('action') || ''
  const object = filters.get('object') || ''
  const result = filters.get('result') || ''
  const start = filters.get('start')
  const end = filters.get('end')
  const at = Date.parse(log.at || log.created_at || '')
  return (!action || (log.action || '').includes(action)) &&
    (!object || (log.object || log.object_id || '').includes(object)) &&
    (!result || (log.result || log.state) === result) &&
    (!start || at >= Date.parse(start)) && (!end || at <= Date.parse(end))
}
