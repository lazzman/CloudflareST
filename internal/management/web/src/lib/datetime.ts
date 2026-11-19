export function systemTimeToISO(value: string, timezone: string | undefined) {
  if (!timezone) throw new Error('系统时区尚未读取，不能换算时间筛选；请稍后重试')
  const parts = /^([0-9]{4}-[0-9]{2}-[0-9]{2})T([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:[.]([0-9]{1,3}))?)?$/.exec(value)
  if (!parts) throw new Error('时间筛选格式无效，请按系统时区重新选择日期与时刻')
  const normalized = `${parts[1]}T${parts[2]}:${parts[3]}:${parts[4] || '00'}.${(parts[5] || '').padEnd(3, '0')}Z`
  const wallTime = Date.parse(normalized)
  if (!Number.isFinite(wallTime) || new Date(wallTime).toISOString() !== normalized) throw new Error('时间筛选包含无效日期或时刻')
  let formatter: Intl.DateTimeFormat
  try {
    formatter = new Intl.DateTimeFormat('en-GB', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
  } catch { throw new Error('系统时区无效，不能换算时间筛选') }
  function localTime(instant: number) {
    const fields = Object.fromEntries(formatter.formatToParts(instant).map(part => [part.type, part.value]))
    return Date.parse(`${fields.year!.padStart(4, '0')}-${fields.month}-${fields.day}T${fields.hour}:${fields.minute}:${fields.second}Z`)
  }
  const wallSeconds = Math.floor(wallTime / 1000) * 1000
  const offsets = new Set([-36, 0, 36].map(hours => {
    const sample = wallSeconds + hours * 3600000
    return localTime(sample) - sample
  }))
  const matches = [...offsets].map(offset => wallTime - offset).filter(instant => localTime(instant) === wallSeconds)
  if (!matches.length) throw new Error('该时刻在系统时区不存在（夏令时跳变），请调整筛选边界')
  if (matches.length > 1) throw new Error('该时刻在系统时区重复（夏令时回拨），请调整筛选边界以避免歧义')
  return new Date(matches[0]!).toISOString()
}

export function instantToSystemInput(instant: number, timezone: string | undefined) {
  if (!timezone) throw new Error('系统时区尚未读取，不能生成时间筛选；请稍后重试')
  let formatter: Intl.DateTimeFormat
  try {
    formatter = new Intl.DateTimeFormat('en-GB', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
  } catch { throw new Error('系统时区无效，不能生成时间筛选') }
  const fields = Object.fromEntries(formatter.formatToParts(instant).map(part => [part.type, part.value]))
  return fields.year!.padStart(4, '0') + '-' + fields.month + '-' + fields.day + 'T' + fields.hour + ':' + fields.minute
}
