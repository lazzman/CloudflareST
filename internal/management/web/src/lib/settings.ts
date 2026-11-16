import { readonly, ref } from 'vue'
import { request } from '../api/client'
import type { Settings } from '../api/types'
import { date as formatDate } from './domain'

const timezone = ref<string>()
let sequence = 0
export const systemTimezone = readonly(timezone)
export async function loadSettings() {
  const current = ++sequence
  const settings = await request<Settings>('/settings')
  if (current === sequence) timezone.value = settings.timezone
  return settings
}
export function clearSettings() { sequence++; timezone.value = undefined }
export function date(value?: string) { return formatDate(value, timezone.value) }
