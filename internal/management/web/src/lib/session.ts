import { reactive } from 'vue'
import { request, setCsrf } from '../api/client'

export const session = reactive({ authenticated: false, ready: false, notice: '', generation: 0 })
export async function checkSession() {
  const result = await request<{ authenticated: boolean; csrf_token?: string }>('/session')
  setCsrf(result.csrf_token)
  session.authenticated = result.authenticated === true
  session.ready = true
  return result
}
export function hideSession(notice = '会话已失效，请重新登录。未确认操作仅可按原号查询，不会自动重交。') {
  session.authenticated = false
  session.generation++
  session.notice = notice
  setCsrf()
}
export async function login(password: string) {
  await checkSession()
  await request('/session', { method: 'POST', body: JSON.stringify({ password }) })
  await checkSession()
  session.notice = ''
}
export async function logout() {
  try {
    await request('/session', { method: 'DELETE' })
    hideSession('已退出当前会话。后台运行、队列和其他使用者不受影响。')
  } catch {
    hideSession('已隐藏本页数据，但未确认服务端退出成功。请重试退出；不要把关闭页面当作会话失效。')
    throw new Error(session.notice)
  }
}
