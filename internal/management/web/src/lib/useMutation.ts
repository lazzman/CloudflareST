import { ref, shallowRef } from 'vue'
import { ApiError, write } from '../api/client'
import type { Receipt } from '../api/types'

export function useMutation() {
  const busy = ref(false)
  const error = shallowRef<ApiError>()
  const receipt = shallowRef<Receipt>()
  async function submit(path: string, options: Parameters<typeof write>[1] = {}) {
    if (busy.value) return
    busy.value = true
    error.value = undefined
    receipt.value = undefined
    try { receipt.value = await write(path, options); return receipt.value }
    catch (caught) { error.value = caught instanceof ApiError ? caught : new ApiError(0, { message: '操作结果未确认，请核对本页数据后重试' }, true) }
    finally { busy.value = false }
  }
  return { busy, error, receipt, submit }
}
