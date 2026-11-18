import { onMounted, onUnmounted, ref, shallowRef, watch, type WatchSource } from 'vue'
import { ApiError } from '../api/client'

export function useResource<Value>(loader: () => Promise<Value>, options: { key?: WatchSource; poll?: (value: Value | undefined) => boolean } = {}) {
  const data = shallowRef<Value>()
  const loading = ref(false)
  const refreshing = ref(false)
  const error = shallowRef<Error>()
  const updatedAt = ref('')
  let stopped = false
  let hasLoaded = false
  let sequence = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  async function read(background: boolean) {
    if (stopped) return
    const current = ++sequence
    clearTimeout(timer)
    const quietly = background && hasLoaded
    loading.value = !quietly
    refreshing.value = quietly
    try {
      const value = await loader()
      if (!stopped && current === sequence) { data.value = value; error.value = undefined; updatedAt.value = new Date().toISOString(); hasLoaded = true }
    } catch (caught) {
      if (!stopped && current === sequence) error.value = caught instanceof Error ? caught : new Error('读取失败')
    } finally {
      if (!stopped && current === sequence) {
        loading.value = false
        refreshing.value = false
        if (options.poll?.(data.value) && !(error.value instanceof ApiError && error.value.status === 401)) {
          timer = setTimeout(() => { if (document.visibilityState !== 'hidden') void read(true); else timer = setTimeout(() => { void read(true) }, 2000) }, 2000)
        }
      }
    }
  }
  function reload() { return read(false) }
  if (options.key) watch(options.key, () => { data.value = undefined; updatedAt.value = ''; error.value = undefined; hasLoaded = false; void reload() })
  onMounted(reload)
  onUnmounted(() => { stopped = true; sequence++; clearTimeout(timer); data.value = undefined })
  return { data, loading, refreshing, error, updatedAt, reload }
}
