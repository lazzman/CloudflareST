import type { OperationLog } from '../api/types'
import { label as domainLabel } from './domain'
import { timeQuery } from './serialize'

export interface LogFilters { action: string; actor: string; object_id: string; result: string; start: string; end: string }
export interface LogOption { value: string; text: string }
export interface LogOptionGroup { label: string; options: LogOption[] }

export const ACTION_GROUPS: LogOptionGroup[] = [
  {
    label: '策略与运行',
    options: [
      { value: 'task_create', text: '新建策略' },
      { value: 'task_update', text: '更新策略' },
      { value: 'task_archive', text: '归档策略' },
      { value: 'task_restore', text: '恢复策略' },
      { value: 'schedule_update', text: '定时启停' },
      { value: 'run_start', text: '触发运行' },
      { value: 'run_cancel', text: '停止运行' },
      { value: 'run_finished', text: '运行结束' },
    ],
  },
  {
    label: 'DNS 与通知',
    options: [
      { value: 'dns_delivery', text: '手动 DNS 投递' },
      { value: 'notification_delivery', text: '手动通知' },
      { value: 'delivery_result', text: '投递结果' },
      { value: 'dns_protection_clear', text: '解除 DNS 保护' },
    ],
  },
  {
    label: '运行历史',
    options: [
      { value: 'history_delete', text: '删除运行历史' },
      { value: 'history_restore', text: '恢复运行历史' },
      { value: 'history_expire', text: '历史到期回收' },
      { value: 'history_purge', text: '回收站清理' },
    ],
  },
  {
    label: '服务连接',
    options: [
      { value: 'connection_create', text: '新增服务连接' },
      { value: 'connection_update', text: '更新服务连接' },
      { value: 'connection_delete', text: '删除服务连接' },
    ],
  },
  {
    label: '系统',
    options: [
      { value: 'settings_update', text: '更新系统设置' },
      { value: 'schedule_trigger', text: '定时触发' },
      { value: 'schedule_skipped', text: '定时跳过' },
      { value: 'restart_recovery', text: '重启恢复' },
      { value: 'operation_recovery', text: '操作核查' },
      { value: 'other', text: '其他动作' },
    ],
  },
]

const resultOption = (value: string, text = domainLabel(value)): LogOption => ({ value, text })
export const RESULT_GROUPS: LogOptionGroup[] = [
  { label: '完成与受理', options: ['accepted', 'completed', 'success', 'restored', 'reviewed'].map(value => resultOption(value)) },
  { label: '失败与异常', options: ['rejected', 'failed', 'unconfirmed', 'interrupted'].map(value => resultOption(value)) },
  { label: '运行状态', options: ['queued', 'running', 'no_results', 'cancelled'].map(value => resultOption(value)).concat(resultOption('canceled', '已取消（旧值）')) },
  { label: '跳过与清理', options: ['skipped', 'cleared', 'recycled', 'removed'].map(value => resultOption(value)) },
]

const ACTION_TEXTS = new Map(ACTION_GROUPS.flatMap(group => group.options.map(option => [option.value, option.text] as const)))
export const actionFilterValues = new Set(ACTION_TEXTS.keys())

export function actionText(log: OperationLog) {
  const kind = log.action_kind || ''
  if (kind && kind !== 'other') return ACTION_TEXTS.get(kind) || kind
  return log.action || '未知动作'
}

export function logQuery(filters: LogFilters, timezone: string | undefined) {
  return timeQuery({ action: filters.action.trim(), actor: filters.actor, object: filters.object_id.trim(), result: filters.result, start: filters.start, end: filters.end }, timezone)
}
export function logMatches(log: OperationLog, filters: URLSearchParams) {
  const action = filters.get('action') || ''
  const actor = filters.get('actor') || ''
  const object = filters.get('object') || ''
  const result = filters.get('result') || ''
  const start = filters.get('start')
  const end = filters.get('end')
  const at = Date.parse(log.at || log.created_at || '')
  const actionMatches = !action || (actionFilterValues.has(action) ? (log.action_kind || '') === action : (log.action || '').includes(action))
  return actionMatches &&
    (!actor || (log.actor || '') === actor) &&
    (!object || (log.object || log.object_id || '').includes(object)) &&
    (!result || (log.result || log.state) === result) &&
    (!start || at >= Date.parse(start)) && (!end || at <= Date.parse(end))
}
