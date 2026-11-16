export interface ScrollPosition { top: number; left: number }

let stopRestoring = () => {}
const locks = new WeakMap<HTMLElement, { count: number; overflow: string }>()

export function scrollOwner(): HTMLElement {
  const content = document.querySelector<HTMLElement>('.workspace > #main-content')
  if (content?.parentElement && getComputedStyle(content.parentElement).overflowY === 'hidden') return content
  return document.scrollingElement as HTMLElement || document.documentElement
}

export function readScrollPosition(): ScrollPosition {
  const owner = scrollOwner()
  return { top: owner.scrollTop, left: owner.scrollLeft }
}

export function routeScrollPosition(saved: ScrollPosition | null, cached: ScrollPosition | undefined, samePath: boolean, current: ScrollPosition): ScrollPosition {
  if (saved) return cached ?? saved
  return samePath ? current : { top: 0, left: 0 }
}

export function cancelScrollRestore() { stopRestoring() }

export function restoreScrollPosition(position: ScrollPosition) {
  cancelScrollRestore()
  let frame = 0
  let stopped = false
  const root = document.getElementById('app')
  const stop = () => {
    if (stopped) return
    stopped = true
    cancelAnimationFrame(frame)
    observer?.disconnect()
    for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.removeEventListener(event, stop, true)
  }
  const apply = () => {
    if (stopped) return
    const main = document.getElementById('main-content')
    if (!main || main.querySelector('[data-resource-loading]')) return
    scrollOwner().scrollTo({ top: position.top, left: position.left, behavior: 'instant' })
    stop()
  }
  const schedule = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(() => apply()) }
  const observer = root ? new MutationObserver(schedule) : undefined
  if (root) observer?.observe(root, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-resource-loading'] })
  for (const event of ['wheel', 'touchstart', 'pointerdown', 'keydown']) window.addEventListener(event, stop, { capture: true, passive: true })
  stopRestoring = stop
  schedule()
}

export function lockBackgroundScroll(): () => void {
  const owner = scrollOwner()
  const navigation = document.querySelector<HTMLElement>('.sidebar nav')
  const elements = [...new Set([document.documentElement, document.body, owner, navigation && (locks.has(navigation) || getComputedStyle(navigation).overflowY === 'auto') ? navigation : null].filter((element): element is HTMLElement => !!element))]
  for (const element of elements) {
    const existing = locks.get(element)
    if (existing) existing.count++
    else { locks.set(element, { count: 1, overflow: element.style.overflow }); element.style.overflow = 'hidden' }
  }
  let unlocked = false
  return () => {
    if (unlocked) return
    unlocked = true
    for (const element of elements) {
      const state = locks.get(element)
      if (!state || --state.count > 0) continue
      element.style.overflow = state.overflow
      locks.delete(element)
    }
  }
}
