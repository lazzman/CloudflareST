import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref, type EffectScope } from 'vue'
import { useResource } from '../src/lib/useResource'
import { ApiError } from '../src/api/client'

const lifecycle = vi.hoisted(() => ({ mounted: [] as (() => void)[], unmounted: [] as (() => void)[] }))
vi.mock('vue', async () => ({
  ...await vi.importActual<typeof import('vue')>('vue'),
  onMounted: (callback: () => void) => { lifecycle.mounted.push(callback) },
  onUnmounted: (callback: () => void) => { lifecycle.unmounted.push(callback) },
}))
const scopes: EffectScope[] = []
function deferred<Value>() {
  let resolve!: (value: Value) => void
  let reject!: (error: Error) => void
  const promise = new Promise<Value>((accept, fail) => { resolve = accept; reject = fail })
  return { promise, resolve, reject }
}
function mount<Value>(loader: () => Promise<Value>, options: Parameters<typeof useResource<Value>>[1] = {}) {
  const scope = effectScope()
  scopes.push(scope)
  const resource = scope.run(() => useResource(loader, options))!
  lifecycle.mounted.shift()!()
  return resource
}
async function settle() { await Promise.resolve(); await nextTick() }
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-11-17T01:00:00Z'))
  vi.stubGlobal('document', { visibilityState: 'visible' })
})
afterEach(() => {
  lifecycle.unmounted.splice(0).forEach(callback => callback())
  scopes.splice(0).forEach(scope => scope.stop())
  lifecycle.mounted.length = 0
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('资源读取与后台刷新', () => {
  it('首次读取显示加载，后续自动刷新保留数据且不切回初始加载', async () => {
    const first = deferred<string>(), second = deferred<string>()
    const loader = vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const resource = mount(loader, { poll: () => true })
    expect(resource.loading.value).toBe(true)
    expect(resource.refreshing.value).toBe(false)
    first.resolve('原数据'); await settle()
    const previousTime = resource.updatedAt.value
    await vi.advanceTimersByTimeAsync(2000)
    expect(loader).toHaveBeenCalledTimes(2)
    expect(resource.loading.value).toBe(false)
    expect(resource.refreshing.value).toBe(true)
    expect(resource.data.value).toBe('原数据')
    expect(resource.updatedAt.value).toBe(previousTime)
    second.resolve('新数据'); await settle()
    expect(resource.data.value).toBe('新数据')
    expect(resource.refreshing.value).toBe(false)
    expect(resource.updatedAt.value).not.toBe(previousTime)
  })
  it('手动刷新仍有加载反馈，刷新完成前保留已有内容', async () => {
    const response = deferred<string>()
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockReturnValueOnce(response.promise)
    const resource = mount(loader)
    await settle()
    const reading = resource.reload()
    expect(resource.loading.value).toBe(true)
    expect(resource.data.value).toBe('原数据')
    expect(resource.refreshing.value).toBe(false)
    response.resolve('手动更新')
    await reading
    expect(resource.loading.value).toBe(false)
    expect(resource.data.value).toBe('手动更新')
  })
  it('切换查询立即清除旧数据与时间，迟到的轮询响应不能覆盖新查询', async () => {
    const key = ref('first'), oldResponse = deferred<string>(), newResponse = deferred<string>()
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockReturnValueOnce(oldResponse.promise).mockReturnValueOnce(newResponse.promise)
    const resource = mount(loader, { key, poll: () => true })
    await settle(); await vi.advanceTimersByTimeAsync(2000)
    key.value = 'second'; await nextTick()
    expect(resource.data.value).toBeUndefined()
    expect(resource.updatedAt.value).toBe('')
    expect(resource.loading.value).toBe(true)
    expect(resource.refreshing.value).toBe(false)
    oldResponse.resolve('迟到旧响应'); await settle()
    expect(resource.data.value).toBeUndefined()
    expect(resource.loading.value).toBe(true)
    newResponse.resolve('新查询'); await settle()
    expect(resource.data.value).toBe('新查询')
  })
  it('手动刷新可以取代在途轮询且不会被其收尾改写状态', async () => {
    const oldResponse = deferred<string>(), manual = deferred<string>()
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockReturnValueOnce(oldResponse.promise).mockReturnValueOnce(manual.promise)
    const resource = mount(loader, { poll: () => true })
    await settle(); await vi.advanceTimersByTimeAsync(2000)
    const reading = resource.reload()
    oldResponse.reject(new Error('旧轮询失败')); await settle()
    expect(resource.loading.value).toBe(true)
    expect(resource.error.value).toBeUndefined()
    manual.resolve('最新手动结果'); await reading
    expect(resource.data.value).toBe('最新手动结果')
    expect(resource.loading.value).toBe(false)
    expect(vi.getTimerCount()).toBe(1)
  })
  it('刷新失败保留最后成功的数据和时间并显示错误，后续成功可恢复', async () => {
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockRejectedValueOnce(new Error('读取失败')).mockResolvedValueOnce('恢复结果')
    const resource = mount(loader, { poll: () => true })
    await settle()
    const previousTime = resource.updatedAt.value
    await vi.advanceTimersByTimeAsync(2000)
    expect(resource.data.value).toBe('原数据')
    expect(resource.updatedAt.value).toBe(previousTime)
    expect(resource.error.value?.message).toBe('读取失败')
    expect(resource.refreshing.value).toBe(false)
    await vi.advanceTimersByTimeAsync(2000)
    expect(resource.data.value).toBe('恢复结果')
    expect(resource.error.value).toBeUndefined()
  })
  it('认证失效后停止自动重试', async () => {
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockRejectedValue(new ApiError(401, { code: 'AUTH_REQUIRED', message: '请重新登录' }))
    const resource = mount(loader, { poll: () => true })
    await settle(); await vi.advanceTimersByTimeAsync(8000)
    expect(loader).toHaveBeenCalledTimes(2)
    expect(resource.error.value).toBeInstanceOf(ApiError)
    expect(vi.getTimerCount()).toBe(0)
  })
  it('未成功读取前的自动重试仍显示加载，不伪造已有数据', async () => {
    const response = deferred<string>()
    const loader = vi.fn().mockRejectedValueOnce(new Error('首次失败')).mockReturnValueOnce(response.promise)
    const resource = mount(loader, { poll: () => true })
    await settle(); await vi.advanceTimersByTimeAsync(2000)
    expect(resource.loading.value).toBe(true)
    expect(resource.refreshing.value).toBe(false)
    expect(resource.updatedAt.value).toBe('')
    response.resolve('恢复'); await settle()
    expect(resource.data.value).toBe('恢复')
  })
  it('卸载后忽略在途结果且不再安排轮询', async () => {
    const response = deferred<string>()
    const loader = vi.fn().mockResolvedValueOnce('原数据').mockReturnValueOnce(response.promise)
    const resource = mount(loader, { poll: () => true })
    await settle(); await vi.advanceTimersByTimeAsync(2000)
    lifecycle.unmounted.shift()!()
    response.resolve('迟到'); await settle(); await vi.advanceTimersByTimeAsync(6000)
    expect(resource.data.value).toBeUndefined()
    expect(loader).toHaveBeenCalledTimes(2)
    expect(vi.getTimerCount()).toBe(0)
  })
})
