import { onMounted, onUnmounted, ref, shallowRef, watch, type WatchSource } from 'vue'
import { ApiError } from '../api/client'

export function useResource<Value>(loader: () => Promise<Value>, options: { key?: WatchSource; poll?: (value: Value | undefined) => boolean } = {}) {
  const data = shallowRef<Value>()
  const loading = ref(false)
  const error = shallowRef<Error>()
  const updatedAt = ref('')
  let stopped = false
  let sequence = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  async function reload() {
    const current = ++sequence
    clearTimeout(timer)
    loading.value = true
    try {
      const value = await loader()
      if (!stopped && current === sequence) { data.value = value; error.value = undefined; updatedAt.value = new Date().toISOString() }
    } catch (caught) {
      if (!stopped && current === sequence) error.value = caught instanceof Error ? caught : new Error('读取失败')
    } finally {
      if (!stopped && current === sequence) {
        loading.value = false
        if (options.poll?.(data.value) && !(error.value instanceof ApiError && error.value.status === 401)) {
          timer = setTimeout(() => { if (document.visibilityState !== 'hidden') void reload(); else timer = setTimeout(reload, 2000) }, 2000)
        }
      }
    }
  }
  if (options.key) watch(options.key, () => { data.value = undefined; void reload() })
  onMounted(reload)
  onUnmounted(() => { stopped = true; sequence++; clearTimeout(timer); data.value = undefined })
  return { data, loading, error, updatedAt, reload }
}
