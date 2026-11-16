import { describe, expect, it } from 'vitest'
import { normalizeTheme } from '../src/lib/appearance'
describe('本地外观偏好', () => {
  it.each(['light', 'dark'] as const)('保留明确的%s模式', value => { expect(normalizeTheme(value)).toBe(value) })
  it.each(['system', '', 'unknown', null, undefined, 1, {}])('未知或缺失偏好%s回退跟随系统', value => { expect(normalizeTheme(value)).toBe('system') })
})
