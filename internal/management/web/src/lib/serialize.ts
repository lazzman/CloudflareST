export function cloneDto<Value>(value: Value): Value {
  return JSON.parse(JSON.stringify(value)) as Value
}

export function timeQuery(values: Record<string, string>, timezone: string | undefined) {
  const result = { ...values }
  for (const key of ['start', 'end']) {
    const raw = result[key]
    if (raw) result[key] = systemTimeToISO(key === 'end' && /^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}$/.test(raw) ? raw + ':59.999' : raw, timezone)
  }
  return result
}
import { systemTimeToISO } from './datetime'
