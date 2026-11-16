export interface OptimizerConfig {
  ipv4_enabled: boolean
  ipv6_enabled: boolean
  sources: Source[]
  sample_size: number
  ipv4_limit: number
  ipv6_limit: number
  probe_mode: string
  tcp_port: number
  probe_count: number
  probe_timeout_ms: number
  latency_enabled: boolean
  loss_enabled: boolean
  download_enabled: boolean
  latency_max_ms: number
  loss_max_percent: number
  speed_min_mb_s: number
  filter_order: string[]
  sort: string
  download_ipv4_count: number
  download_ipv6_count: number
  download_window_seconds: number
  prebody_timeout_seconds: number
  download_url: string
}
export type Download4Field = 'download\u005fipv4\u005fcount'
export type Download6Field = 'download\u005fipv6\u005fcount'
export const DOWNLOAD4_FIELD: Download4Field = ('download' + String.fromCharCode(95) + 'ipv4' + String.fromCharCode(95) + 'count') as Download4Field
export const DOWNLOAD6_FIELD: Download6Field = ('download' + String.fromCharCode(95) + 'ipv6' + String.fromCharCode(95) + 'count') as Download6Field
export interface Source { kind: string; value: string; count?: number }
export interface Schedule {
  enabled?: boolean
  kind?: 'interval' | 'daily' | 'weekly' | 'cron'
  expression?: string
  interval_minutes?: number
  time_of_day?: string
  weekday?: number
  next_run_at?: string
}
export interface NotificationConfig { enabled?: boolean; connection_id?: string }
export interface Task {
  latest_run?: Pick<Run, 'id' | 'task_id' | 'status' | 'queued_at' | 'ended_at'> | null
  configuration_issue?: string
  id: string
  name?: string
  revision?: number | string
  archived?: boolean
  config?: Partial<OptimizerConfig>
  schedule?: Schedule
  dns_auto?: boolean
  targets?: Target[]
  notifications?: { telegram?: NotificationConfig; wecom?: NotificationConfig }
}
export interface Protection { active?: boolean; revision?: number | string; reason?: string; created_at?: string; attempt_id?: string }
export interface Target {
  connection_id: string
  account_id?: string
  record_id: string
  domain?: string
  rr?: string
  type?: string
  line?: string
  ttl?: number
  value?: string
  target_id?: string
  read_at?: string
  last_known_write?: { task_id?: string; run_id?: string; written_at?: string; ip?: string }
  protection?: Protection
}
export interface Decision { stage?: string; state?: string; reason?: string }
export interface Candidate {
  ip: string
  family?: string
  sources?: string[]
  latency_ms?: number | null
  loss_percent?: number | null
  speed_mb_s?: number | null
  eligible?: boolean
  rank?: number
  state?: string
  reason?: string
  steps?: Decision[]
  download_bytes?: number
  download_seconds?: number
  download_end?: string
  origin?: string
  probe_attempts?: number
  probe_successes?: number
  probe_failures?: number
}
export interface Progress { stage?: string; family?: string; done?: number; total?: number | null; unit?: string; message?: string }
export interface Budget {
  family?: string
  limit?: number
  directed?: number
  initial_limit?: number
  sampled?: number
  expansion_limit?: number
  expanded?: number
  used?: number
  reason?: string
  requested_initial?: number
  valid_initial?: number
  trigger_count?: number
  remaining?: number
  initial_reason?: string
  expansions?: unknown[]
}
export interface Delivery {
  id?: string
  attempt_id?: string
  operation_id?: string
  kind?: string
  channel?: string
  state?: string
  status?: string
  reason?: string
  message?: string
  created_at?: string
  connection_id?: string
  prior_attempt_id?: string
  target?: Target
  ip?: string
  verified?: boolean
  no_write?: boolean
  automatic?: boolean
  cancellation?: boolean
  started_at?: string
  ended_at?: string
  before?: { value: string; at: string }
  readback?: { value: string; at: string }
}
export interface Page<Value> { items: Value[]; total: number; page: number; page_size: number }
export interface FamilySummary { family: string; count: number; eligible: number; best?: Candidate }
export interface CandidateSummary { total: number; families: FamilySummary[] }
export interface StageCounts { succeeded: number; failed: number; skipped: number; filtered: number; unconfirmed: number }
export interface StageProgress extends Progress { counts?: StageCounts; qualified?: number }
export interface ProgressSnapshot { id: string; status?: string; stage?: string; slot_busy?: boolean; progress?: StageProgress; stage_progress?: StageProgress[]; progress_revision?: number; result_status?: string; candidate_summary?: CandidateSummary; deliveries?: Delivery[]; server_time?: string; updated_at?: string }
export interface SourceResult { id?: string; source?: Source; status?: string; error?: string; resolved_ips?: string[]; cidrs?: string[]; candidate_count?: number }
export interface Run {
  reason?: string
  id: string
  task_id: string
  task_name?: string
  trigger?: string
  status?: string
  stage?: string
  slot_busy?: boolean
  queued_at?: string
  started_at?: string
  ended_at?: string
  deleted_at?: string
  retention_at?: string
  restore_until?: string
  progress?: Progress
  stage_progress?: StageProgress[]
  candidate_summary?: CandidateSummary
  updated_at?: string
  server_time?: string
  result?: { status?: string; candidates?: Candidate[]; budgets?: Budget[]; sources?: SourceResult[]; error?: string }
  deliveries?: Delivery[]
  snapshot?: Task
}
export interface Queue { current: Run | null; waiting: Run[]; server_time?: string }
export type ConnectionKind = 'alidns' | 'telegram' | 'wecom'
export interface Connection {
  id: string
  kind: ConnectionKind
  name?: string
  account_id?: string
  credentials_set?: boolean
  revision?: string | number
  referenced_by?: (string | { id?: string; task_id?: string; name?: string })[]
}
export interface Defaults { config: OptimizerConfig; timezone?: string; history_days?: number }
export interface Settings { timezone?: string; history_days?: number; icmp?: { available?: boolean; message?: string }; password_source?: string }
export interface RecoveryReceipt { id: string; prior_operation_id: string; path: string; method: string; consumed_by?: string }
export interface Receipt { operation_id: string; object_id?: string; state?: string; result?: unknown; prior_operation_id?: string; recovery_id?: string; recovery_operation_id?: string; recovery?: RecoveryReceipt; action?: string }
export interface OperationLog { id?: string; operation_id?: string; created_at?: string; at?: string; action?: string; actor?: string; object_id?: string; object?: string; state?: string; result?: string; reason?: string; message?: string }
export interface ApiErrorBody { code?: string; message?: string; fields?: Record<string, string | string[]>; operation_id?: string; action?: string }
