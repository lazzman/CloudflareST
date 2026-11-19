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
export interface PendingOperation { id: string; scope?: OperationScope }
const scopes = new Map<string, OperationScope>()
const validScope = (scope: OperationScope) => ['POST','PATCH','DELETE'].includes(scope.method) && /^\/api\/v1\/(tasks(?:\/[A-Za-z0-9_-]+(?:\/(?:runs|archive|restore|schedule-state))?)?|connections(?:\/[A-Za-z0-9_-]+)?|runs\/[A-Za-z0-9_-]+(?:\/(?:cancel|restore|dns-deliveries|notifications))?|settings|dns-targets\/[A-Za-z0-9_-]+\/auto-write-protection\/clear)$/.test(scope.path)
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i
const fingerprintPattern = /^[0-9a-f]{64}$/

export function setTransport(next: Transport) { transport = next }
export function setCsrf(token?: string) { csrfToken = token || '' }
export function setUnauthorizedHandler(handler: () => void) { onUnauthorized = handler }
export function getPendingIds() { return [...pending] }
export function getPendingOperations(scope?: string, exact = false): PendingOperation[] {
  return getPendingIds()
    .map(id => ({ id, scope: scopes.get(id) }))
    .filter(item => {
      if (!scope) return true
      if (!item.scope) return false
      return exact ? item.scope.path === scope : item.scope.path === scope || item.scope.path.startsWith(scope + '/')
    })
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
    const entries: unknown[] = stored?.version === 2 && Array.isArray(stored.operations) ? stored.operations : []
    for (const entry of entries) {
      if (!entry || typeof entry !== 'object') continue
      const item = entry as { operation_id?: unknown; fingerprint?: unknown; scope?: OperationScope }
      if (typeof item.operation_id !== 'string' || !uuidPattern.test(item.operation_id)) continue
      if (!item.scope || !validScope(item.scope)) continue
      pending.add(item.operation_id)
      scopes.set(item.operation_id, item.scope)
      if (typeof item.fingerprint === 'string' && fingerprintPattern.test(item.fingerprint)) actionLocks.set(item.fingerprint, item.operation_id)
    }
  } catch {}
  listeners.forEach(listener => listener())
}
function persist() {
  const fingerprints = new Map([...actionLocks].map(([fingerprint, id]) => [id, fingerprint]))
  try { localStorage.setItem(OPERATION_STORAGE_KEY, JSON.stringify({ version: 2, operations: getPendingIds().map(operation_id => ({ operation_id, fingerprint: fingerprints.get(operation_id), scope: scopes.get(operation_id) })) })) } catch { return false }
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
    if (scope) scopes.set(id, scope)
    for (const [fingerprint, operationId] of locks) actionLocks.set(fingerprint, operationId)
  }
}
export function newOperationId(fingerprint?: string, scope?: OperationScope) {
  const id = randomOperationId()
  const previousId = fingerprint ? actionLocks.get(fingerprint) : undefined
  pending.add(id)
  if (scope && validScope(scope)) scopes.set(id, scope)
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
      throw new ApiError(response.status, { code: 'INVALID_RESPONSE', message: '服务端未返回有效 JSON，系统将自动查询上次结果' }, method !== 'GET')
    }
    if (!response.ok) {
      if (response.status === 401) { setCsrf(); onUnauthorized() }
      throw new ApiError(response.status, (body || {}) as ApiErrorBody, method !== 'GET' && response.status >= 500)
    }
    return body as Result
  } catch (error) {
    if (error instanceof ApiError) throw error
    throw new ApiError(0, { code: 'NETWORK_ERROR', message: method === 'GET' ? '连接中断，以下可能为上次读取的数据。请重试读取。' : '未能确认写入结果，系统将自动查询上次结果。' }, method !== 'GET')
  } finally { clearTimeout(timeout) }
}
export function isUncertain(state?: string) { return typeof state !== 'string' || !state || ['unknown', 'expired', 'unconfirmed', 'pending', 'retired'].includes(state.toLowerCase()) }
function credentialBody(body?: object) {
  if (!body || typeof body !== 'object') return body
  const connection = (body as { connection?: Record<string, unknown> }).connection
  if (!connection || typeof connection !== 'object') return body
  const { secrets, ...rest } = connection
  return { ...body, connection: { ...rest, secret_keys: secrets && typeof secrets === 'object' ? Object.keys(secrets).sort() : [] } }
}
async function actionFingerprint(path: string, options: { method?: string; body?: object; revision?: string | number }) {
  const scope = { path: apiPath(path), method: options.method || 'POST' }
  const pathname = scope.path.split('?')[0]!
  const credentials = pathname === '/api/v1/session' || pathname === '/api/v1/connections' || pathname.startsWith('/api/v1/connections/')
  const serialized = JSON.stringify(credentials
    ? { path: pathname, method: scope.method, ...(pathname === '/api/v1/session' ? {} : { body: credentialBody(options.body) }) }
    : { ...scope, revision: options.revision, body: options.body }, (_key, value: unknown) => {
    if (value && typeof value === 'object' && !Array.isArray(value)) return Object.fromEntries(Object.entries(value).sort(([first], [second]) => first.localeCompare(second)))
    return value
  })
  const bytes = new TextEncoder().encode(serialized)
  const digest = typeof globalThis.crypto?.subtle?.digest === 'function'
    ? new Uint8Array(await globalThis.crypto.subtle.digest('SHA-256', bytes))
    : sha256(bytes)
  return Array.from(digest, value => value.toString(16).padStart(2, '0')).join('')
}
type PendingResolution =
  | { status: 'confirmed'; receipt: Receipt }
  | { status: 'missing' }
  | { status: 'unresolved'; error: ApiError }
async function resolvePending(id: string): Promise<PendingResolution> {
  try {
    const receipt = await lookupOperation(id)
    if (!isUncertain(receipt.state)) return { status: 'confirmed', receipt }
    return { status: 'unresolved', error: new ApiError(409, { code: 'OPERATION_UNCONFIRMED', message: '上次写入结果仍未确认，请核对本页数据后再操作。', operation_id: id }, true) }
  } catch (error) {
    if (error instanceof ApiError) {
      if (error.status === 404 && ['operation_unknown', 'not_found'].includes((error.details.code || '').toLowerCase())) return { status: 'missing' }
      error.details.operation_id = id
      error.uncertain = true
      return { status: 'unresolved', error }
    }
    return { status: 'unresolved', error: new ApiError(0, { code: 'OPERATION_LOOKUP_FAILED', message: '上次写入结果查询中断，请核对本页数据后重试。', operation_id: id }, true) }
  }
}
function sendWrite(path: string, options: { method?: 'POST' | 'PATCH' | 'DELETE'; body?: object; revision?: string | number }, operationId: string) {
  const method = options.method || 'POST'
  const headers: Record<string, string> = { 'Idempotency-Key': operationId }
  if (options.revision !== undefined) headers['If-Match'] = String(options.revision)
  return request<Receipt>(path, { method, headers, ...(method === 'DELETE' ? {} : { body: JSON.stringify({ ...options.body, operation_id: operationId }) }) })
}
export async function write(path: string, options: { method?: 'POST' | 'PATCH' | 'DELETE'; body?: object; revision?: string | number } = {}) {
  const fingerprint = await actionFingerprint(path, options)
  const pathname = apiPath(path).split('?')[0]!
  const method = options.method || 'POST'
  const scope = { path: pathname, method }
  const previousId = actionLocks.get(fingerprint)
  let operationId: string
  if (previousId) {
    const previous = await resolvePending(previousId)
    if (previous.status === 'confirmed') return previous.receipt
    if (previous.status === 'unresolved') throw previous.error
    operationId = previousId
  } else {
    operationId = newOperationId(fingerprint, scope)
  }
  try {
    const receipt = await sendWrite(path, options, operationId)
    if (receipt?.operation_id !== operationId) throw new ApiError(202, { code: 'RECEIPT_MISMATCH', message: '收据操作号不匹配，系统将查询上次结果。', operation_id: operationId }, true)
    if (isUncertain(receipt.state)) {
      const resolved = await resolvePending(operationId)
      if (resolved.status === 'confirmed') return resolved.receipt
      if (resolved.status === 'unresolved') throw resolved.error
      throw new ApiError(202, { code: 'OPERATION_UNCONFIRMED', message: '服务端尚未确认写入结果，请核对本页数据。', operation_id: operationId }, true)
    }
    forgetOperation(operationId)
    return receipt
  } catch (error) {
    if (!(error instanceof ApiError)) throw error
    error.details.operation_id = operationId
    if (!error.uncertain) { forgetOperation(operationId); throw error }
    const resolved = await resolvePending(operationId)
    if (resolved.status === 'confirmed') return resolved.receipt
    if (resolved.status === 'unresolved') throw resolved.error
    throw new ApiError(0, { code: 'OPERATION_NOT_FOUND', message: '未找到上次写入结果。可再次提交，系统会沿用原操作号，不会生成新号。', operation_id: operationId }, true)
  }
}
export async function lookupOperation(id: string) {
  let receipt: Receipt
  try { receipt = await request<Receipt>('/operations/' + segment(id)) }
  catch (error) {
    if (error instanceof ApiError && error.status === 410 && error.details.operation_id === id) rememberScope(id, error.details.action)
    throw error
  }
  if (receipt.operation_id !== id) throw new ApiError(200, { code: 'RECEIPT_MISMATCH', message: '收据操作号不匹配，保留原号。' })
  rememberScope(id, receipt.action)
  if (!isUncertain(receipt.state)) forgetOperation(id)
  return receipt
}
function rememberScope(id: string, action?: string) {
  if (!pending.has(id) || !action) return
  const [method, path] = action.trim().split(/\s+/)
  if (!method || !path || !validScope({ method, path })) return
  const previous = scopes.get(id)
  scopes.set(id, { method, path })
  if (!persist()) { if (previous) scopes.set(id, previous); else scopes.delete(id) }
}
