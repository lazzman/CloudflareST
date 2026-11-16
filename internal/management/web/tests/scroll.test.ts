import { describe, expect, it } from 'vitest'
import { routeScrollPosition } from '../src/lib/scroll'

describe('路由滚动目标', () => {
  const current = { top: 740, left: 0 }
  it('进入不同页面时回到顶部，不复用以前访问过的偏移', () => {
    expect(routeScrollPosition(null, { top: 1200, left: 0 }, false, current)).toEqual({ top: 0, left: 0 })
  })
  it('同路径筛选或分页不主动跳到顶部', () => {
    expect(routeScrollPosition(null, undefined, true, current)).toEqual(current)
  })
  it('浏览器前进后退优先恢复内容区，而不是外层窗口的零偏移', () => {
    expect(routeScrollPosition({ top: 0, left: 0 }, current, false, { top: 0, left: 0 })).toEqual(current)
  })
  it('无内容区缓存时使用浏览器保存的页面偏移', () => {
    expect(routeScrollPosition(current, undefined, false, { top: 0, left: 0 })).toEqual(current)
  })
  it('同路径前进后退仍恢复对应历史条目的位置', () => {
    expect(routeScrollPosition({ top: 0, left: 0 }, { top: 320, left: 0 }, true, current)).toEqual({ top: 320, left: 0 })
  })
})
