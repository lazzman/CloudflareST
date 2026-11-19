import { sha256 } from '@noble/hashes/sha2.js'
import type { ApiErrorBody, Receipt } from './types'

export class ApiError extends Error {
  constructor(public status: number, public details: ApiErrorBody, public uncertain = false) {
    super(details.message || '请求失败，请保留当前输入后重试读取')
  }
}
type Transport = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>
let transport: Transport = (input, init) => fetch(input, init)
let csrfToken = ''
let onUnauthorized = () => {}
export const OPERATION_STORAGE_KEY = 'cloudflarest.pending-operation-ids'
const pending = new Set<string>()
const actionLocks = new Map<string, string>()
const listeners = new Set<() => void>()
export interface OperationScope { path: string; method: string }
interface RecoveryPermit extends OperationScope { id: string; prior_operation_id: string; consumed_by?: string }
const scopes = new Map<string, OperationScope>()
const permits = new Map<string, RecoveryPermit>()
const scopeKey = (scope: OperationScope) => scope.method + ' ' + scope.path
const validScope = (scope: OperationScope) => ['POST','PATCH','DELETE'].includes(scope.method) && /^\/api\/v1\/(tasks(?:\/[A-Za-z0-9_-]+(?:\/(?:runs|archive|restore|schedule-state))?)?|connections(?:\/[A-Za-z0-9_-]+)?|runs\/[A-Za-z0-9_-]+(?:\/(?:cancel|restore|dns-deliveries|notifications))?|settings|dns-targets\/[A-Za-z0-9_-]+\/auto-write-protection\/clear)$/.test(scope.path)
export function getOperationScope(id: string) { return scopes.get(id) }
export function getRecoveryPermits() { return [...permits.values()] }
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const fingerprintPattern = /^[0-9a-f]{64}$/

export function setTransport(next: Transport) { transport = next }
export function setCsrf(token?: string) { csrfToken = token || '' }
export function setUnauthorizedHandler(handler: () => void) { onUnauthorized = handler }
export function getPendingIds() { return [...pending] }
export function getUnscopedIds() {
  const scopedIds = new Set(actionLocks.values())
  return getPendingIds().filter(id => !scopedIds.has(id) && !scopes.has(id))
}
export function watchOperations(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } }
function randomOperationId() {
  const capabilities = globalThis.crypto
  if (typeof capabilities?.randomUUID === 'function') return capabilities.randomUUID()
  if (typeof capabilities?.getRandomValues !== 'function') {
    throw new ApiError(0, { code: 'BROWSER_RANDOM_UNAVAILABLE', message: '当前浏览器无法生成安全随机操作号，本次未提交。请更新浏览器或检查其安全限制。' })
  }
  const bytes = capabilities.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6]! & 0x0f) | 0x40
  bytes[8] = (bytes[8]! & 0x3f) | 0x80
  const hex = Array.from(bytes, value => value.toString(16).padStart(2, '0')).join('')
  return [hex.slice(0, 8), hex.slice(8, 12), hex.slice(12, 16), hex.slice(16, 20), hex.slice(20)].join('-')
}
export function restoreOperationIds() {
  try {
    const stored = JSON.parse(localStorage.getItem(OPERATION_STORAGE_KEY) || '[]')
    const entries: unknown[] = Array.isArray(stored) ? stored : stored?.version === 2 && Array.isArray(stored.operations) ? stored.operations : []
    for (const entry of entries) {
      const item = typeof entry === 'string' ? { operation_id: entry } : entry as { operation_id?: unknown; fingerprint?: unknown; scope?: OperationScope } | null
      if (!item || typeof item.operation_id !== 'string' || !uuidPattern.test(item.operation_id)) continue
      pending.add(item.operation_id)
      if (typeof item.fingerprint === 'string' && fingerprintPattern.test(item.fingerprint)) actionLocks.set(item.fingerprint, item.operation_id)
      if (item.scope && validScope(item.scope)) scopes.set(item.operation_id,item.scope)
    }
    for (const permit of stored?.recoveries || []) if (permit && !permit.consumed_by && uuidPattern.test(permit.id) && uuidPattern.test(permit.prior_operation_id) && validScope(permit)) permits.set(scopeKey(permit),permit)
  } catch {}
  listeners.forEach(listener => listener())
}
function persist() {
  const fingerprints = new Map([...actionLocks].map(([fingerprint, id]) => [id, fingerprint]))
  try { localStorage.setItem(OPERATION_STORAGE_KEY, JSON.stringify({ version: 2, operations: getPendingIds().map(operation_id => ({ operation_id, fingerprint: fingerprints.get(operation_id), scope:scopes.get(operation_id) })), ...(permits.size ? {recoveries:getRecoveryPermits()} : {}) })) } catch { return false }
  listeners.forEach(listener => listener())
  return true
}
export function forgetOperation(id: string) {
  const wasPending = pending.has(id)
  const scope = scopes.get(id)
  scopes.delete(id)
  const locks = [...actionLocks].filter(([, operationId]) => operationId === id)
  pending.delete(id)
  for (const [fingerprint] of locks) actionLocks.delete(fingerprint)
  if (!persist()) {
    if (wasPending) pending.add(id)
    if (scope) scopes.set(id,scope)
    for (const [fingerprint, operationId] of locks) actionLocks.set(fingerprint, operationId)
  }
}
export function newOperationId(fingerprint?: string, scope?: OperationScope) {
  const id = randomOperationId()
  const previousId = fingerprint ? actionLocks.get(fingerprint) : undefined
  pending.add(id)
  if (scope && validScope(scope)) scopes.set(id,scope)
  if (fingerprint) actionLocks.set(fingerprint, id)
  if (!persist()) {
    pending.delete(id)
    scopes.delete(id)
    if (fingerprint) {
      if (previousId) actionLocks.set(fingerprint, previousId)
      else actionLocks.delete(fingerprint)
    }
    throw new ApiError(0, { code: 'OPERATION_STORAGE_FAILED', message: '无法保留操作号，本次未提交。请检查浏览器存储权限或可用空间后再操作。' })
  }
  return id
}
export function apiPath(path: string) {
  if (!path.startsWith('/') || path.startsWith('//') || path.includes(String.fromCharCode(92)) || path.includes('#') || [...path].some(char => char.charCodeAt(0) < 32)) throw new Error('仅允许同源 /api/v1 接口')
  const url = new URL('/api/v1' + path, 'http://same-origin.invalid')
  if (!url.pathname.startsWith('/api/v1/')) throw new Error('接口路径越界')
  return url.pathname + url.search
}
export function query(values: Record<string, string | number | undefined>) {
  const params = new URLSearchParams()
  Object.entries(values).forEach(([key, value]) => { if (value !== '' && value !== undefined) params.set(key, String(value)) })
  return params.size ? '?' + params : ''
}
export const segment = (value: string) => encodeURIComponent(value)
export async function request<Result>(path: string, init: RequestInit = {}): Promise<Result> {
  const method = init.method || 'GET'
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')
  if (method !== 'GET') headers.set('X-CSRF-Token', csrfToken)
  const controller = new AbortController()
  const timeout = setTimeout(() => controller.abort(), 15000)
  try {
    const response = await transport(apiPath(path), { ...init, method, headers, credentials: 'same-origin', redirect: 'error', signal: controller.signal })
    let body: unknown
    try { body = response.status === 204 ? undefined : await response.json() } catch {
      throw new ApiError(response.status, { code: 'INVALID_RESPONSE', message: '服务端未返回有效 JSON，写入结果需按原操作号核查' }, method !== 'GET')
    }
    if (!response.ok) {
      if (response.status === 401) { setCsrf(); onUnauthorized() }
      throw new ApiError(response.status, (body || {}) as ApiErrorBody, method !== 'GET' && response.status >= 500)
    }
    return body as Result
  } catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError(0, { code: 'NETWORK_ERROR', message: method === 'GET' ? '连接中断，以下可能为上次读取的数据。请重试读取。' : '未能确认写入结果，请使用原操作号只读核查；不要重新提交。' }, method !== 'GET')
  } finally { clearTimeout(timeout) }
}
export function isUncertain(state?: string) { return typeof state !== 'string' || !state || ['unknown', 'expired', 'unconfirmed', 'pending', 'retired'].includes(state.toLowerCase()) }
async function actionFingerprint(path: string, options: { method?: string; body?: object; revision?: string | number }) {
  const scope = { path: apiPath(path), method: options.method || 'POST' }
  const pathname = scope.path.split('?')[0]!
  const credentials = pathname === '/api/v1/session' || pathname === '/api/v1/connections' || pathname.startsWith('/api/v1/connections/')
  const serialized = JSON.stringify(credentials ? { path: pathname, method: scope.method } : { ...scope, revision: options.revision, body: options.body }, (_key, value: unknown) => {
    if (value && typeof value === 'object' && !Array.isArray(value)) return Object.fromEntries(Object.entries(value).sort(([first], [second]) => first.localeCompare(second)))
    return value
  })
  const bytes = new TextEncoder().encode(serialized)
  const digest = typeof globalThis.crypto?.subtle?.digest === 'function'
    ? new Uint8Array(await globalThis.crypto.subtle.digest('SHA-256', bytes))
    : sha256(bytes)
  return Array.from(digest, value => value.toString(16).padStart(2, '0')).join('')
}
export async function write(path: string, options: { method?: 'POST' | 'PATCH' | 'DELETE'; body?: object; revision?: string | number } = {}) {
  const fingerprint = await actionFingerprint(path, options)
  const pathname = apiPath(path).split('?')[0]!
  const unscopedId = (options.method || 'POST') === 'POST' && ['/api/v1/tasks', '/api/v1/connections'].includes(pathname) ? getUnscopedIds()[0] : undefined
  const knownFingerprintIDs = new Set(actionLocks.values())
  const scopedLegacyID = getPendingIds().find(id => !knownFingerprintIDs.has(id) && scopes.get(id)?.path===pathname && scopes.get(id)?.method===(options.method || 'POST'))
  const previousId = actionLocks.get(fingerprint) || scopedLegacyID || unscopedId
  if (previousId) throw new ApiError(409, { code: 'ACTION_UNCONFIRMED', message: unscopedId ? '旧版待核查记录缺少动作范围，请先只读核查原操作号；确认前不能新建任务或连接，其他对象操作仍可继续。' : '本动作范围已提交且尚未确认，请只读查询原操作号，不换号重交；无关操作仍可继续。', operation_id: previousId }, true)
  const method = options.method || 'POST'
  const scope = {path:pathname,method}
  const permit = permits.get(scopeKey(scope))
  const operationId = newOperationId(fingerprint,scope)
  if (permit) {
    permits.delete(scopeKey(scope))
    if (!persist()) { permits.set(scopeKey(scope),permit); forgetOperation(operationId); throw new ApiError(0,{code:'OPERATION_STORAGE_FAILED',message:'无法保存人工新尝试关联，本次未提交，请核查本机存储'}) }
  }
  const headers: Record<string, string> = { 'Idempotency-Key': operationId }
  if (permit) { headers['X-Prior-Operation-ID']=permit.prior_operation_id;headers['X-Recovery-ID']=permit.id }
  if (options.revision !== undefined) headers['If-Match'] = String(options.revision)
  try {
    const receipt = await request<Receipt>(path, { method, headers, ...(method === 'DELETE' ? {} : { body: JSON.stringify({ ...options.body, operation_id: operationId, ...(permit ? {prior_operation_id:permit.prior_operation_id,recovery_id:permit.id} : {}) }) }) })
    if (receipt?.operation_id !== operationId) throw new ApiError(202, { code: 'RECEIPT_MISMATCH', message: '收据操作号不匹配，请核查原操作号', operation_id: operationId }, true)
    if (!isUncertain(receipt.state) && !pathname.endsWith('/recovery')) forgetOperation(operationId)
    return receipt
  } catch (error) {
    if (error instanceof ApiError) {
      if (!error.uncertain && !/(unknown|expired|unconfirmed|retired)/i.test(error.details.code || '')) {
        if (permit) { permits.set(scopeKey(scope),permit);persist() }
        forgetOperation(operationId)
      }
      else error.uncertain = true
      error.details.operation_id = operationId
    }
    throw error
  }
}
export async function lookupOperation(id: string) {
  let receipt: Receipt
  try { receipt = await request<Receipt>('/operations/' + segment(id)) }
  catch(error) {
    if (error instanceof ApiError && error.status===410 && error.details.operation_id===id) rememberScope(id,error.details.action)
    throw error
  }
  if (receipt.operation_id !== id) throw new ApiError(200, { code: 'RECEIPT_MISMATCH', message: '查询收据身份不匹配，保留原号' })
  rememberScope(id,receipt.action)
  if (!acceptRecovery(receipt) && !isUncertain(receipt.state)) forgetOperation(id)
  return receipt
}

function rememberScope(id: string, action?: string) {
  if (!pending.has(id) || !action) return
  const [method,path] = action.trim().split(/\s+/)
  if (!method || !path || !validScope({method,path})) return
  const previous = scopes.get(id)
  scopes.set(id,{method,path})
  if (!persist()) {if(previous)scopes.set(id,previous);else scopes.delete(id)}
}

function acceptRecovery(receipt: Receipt) {
  const value = receipt.recovery || receipt.result as Partial<RecoveryPermit> | undefined
  if (!value?.id || !value.prior_operation_id || ![value.id,value.prior_operation_id].includes(receipt.operation_id) || !value.path || !value.method || !validScope(value as RecoveryPermit)) return false
  const permit = value as RecoveryPermit
  const previous = permits.get(scopeKey(permit))
  const locks = [...actionLocks].filter(([,id])=>id===permit.prior_operation_id)
  const originalScope = scopes.get(permit.prior_operation_id)
  const originalPending = pending.has(permit.prior_operation_id)
  if (!permit.consumed_by) permits.set(scopeKey(permit),permit)
  else if (previous?.id===permit.id) permits.delete(scopeKey(permit))
  pending.delete(permit.prior_operation_id);scopes.delete(permit.prior_operation_id)
  for (const [key] of locks) actionLocks.delete(key)
  if (!persist()) {
    if (previous) permits.set(scopeKey(permit),previous);else permits.delete(scopeKey(permit))
    if (originalPending) pending.add(permit.prior_operation_id)
    if (originalScope) scopes.set(permit.prior_operation_id,originalScope)
    for (const [key,id] of locks) actionLocks.set(key,id)
    throw new ApiError(0,{code:'OPERATION_STORAGE_FAILED',message:'核查已受理，但本机无法保存恢复许可，请保留核查号并只读查询',operation_id:receipt.operation_id},true)
  }
  forgetOperation(permit.id)
  return true
}

export async function prepareOperationRecovery(id: string, scope: OperationScope, acknowledged: boolean) {
  if (!pending.has(id) || !validScope(scope) || !acknowledged) throw new ApiError(400,{code:'RECOVERY_REVIEW_REQUIRED',message:'请先核查原对象、选择明确范围并确认重复风险'})
  try {
    const original = await lookupOperation(id)
    if (original.recovery) return original
    if (!['unconfirmed','unknown','expired'].includes(original.state || '')) throw new ApiError(409,{code:'RECOVERY_NOT_NEEDED',message:'原操作已受理或已有明确结论，请查看原对象，不创建恢复许可'})
  } catch (error) {
    if (!(error instanceof ApiError) || ![404,410].includes(error.status) || !['operation_unknown','operation_expired'].includes(error.details.code || '')) throw error
  }
  const path = '/operations/'+segment(id)+'/recovery'
  const body = {...scope,risk_acknowledged:true}
  const reviewFingerprint = await actionFingerprint(path,{body})
  const existingReview = actionLocks.get(reviewFingerprint)
  let receipt: Receipt
  if (existingReview) {
    try {
      const originalReview = await lookupOperation(existingReview)
      if (originalReview.recovery) return originalReview
      throw new ApiError(409,{code:'RECOVERY_REVIEW_PENDING',message:'核查收据已有状态，请按原核查号确认，不创建新号',operation_id:existingReview})
    } catch (error) {
      if (!(error instanceof ApiError) || error.status!==404 || error.details.code!=='operation_unknown') throw error
    }
    try {
      receipt = await request<Receipt>(path,{method:'POST',headers:{'Idempotency-Key':existingReview},body:JSON.stringify({...body,operation_id:existingReview})})
      if(receipt.operation_id!==existingReview)throw new ApiError(202,{code:'RECEIPT_MISMATCH',message:'核查收据身份不符，请保留原核查号',operation_id:existingReview},true)
    } catch(error) {
      if(error instanceof ApiError)error.details.operation_id=existingReview
      throw error
    }
  } else receipt = await write(path,{body})
  if (!acceptRecovery(receipt)) throw new ApiError(202,{code:'RECOVERY_RECEIPT_INVALID',message:'核查收据结构未确认，请按原核查号读取',operation_id:receipt.operation_id},true)
  return receipt
}
