import { createApp } from 'vue'
import App from './App.vue'
import { router } from './router'
import { restoreOperationIds, setTransport, setUnauthorizedHandler } from './api/client'
import { hideSession } from './lib/session'
import { initializeAppearance } from './lib/appearance'
import './style.css'
initializeAppearance()

if (__MOCK_API__) {
  const { mockFetch } = await import('./api/mock')
  setTransport(mockFetch)
}
restoreOperationIds()
setUnauthorizedHandler(() => {
  const current = router.currentRoute.value
  hideSession()
  if (current.path !== '/login') void router.replace({ path: '/login', query: { return: current.fullPath } })
})
createApp(App).use(router).mount('#app')
