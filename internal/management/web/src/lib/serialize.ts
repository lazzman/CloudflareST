export function cloneDto<Value>(value: Value): Value {
  return JSON.parse(JSON.stringify(value)) as Value
}

export function timeQuery(values: Record<string, string>, timezone: string | undefined) {
  const result = { ...values }
  for (const key of ['start', 'end']) {
    const raw = result[key]
    if (raw) result[key] = systemTimeToISO(raw, timezone)
  }
  return result
}
import { systemTimeToISO } from './datetime'
