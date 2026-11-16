import { createRouter, createWebHashHistory } from 'vue-router'
import { checkSession, session } from './lib/session'
import { cancelScrollRestore, readScrollPosition, restoreScrollPosition, routeScrollPosition, type ScrollPosition } from './lib/scroll'

const positions = new Map<number, ScrollPosition>()
let activeEntry: number | undefined
let previousPosition: ScrollPosition = { top: 0, left: 0 }

export const router = createRouter({
  history: createWebHashHistory(),
  routes: [
    { path: '/', redirect: '/tasks' },
    { path: '/login', component: () => import('./pages/LoginPage.vue'), meta: { title: '密码登录' } },
    { path: '/tasks', component: () => import('./pages/TaskListPage.vue'), meta: { title: '优选策略' } },
    { path: '/tasks/new', component: () => import('./pages/TaskEditorPage.vue'), meta: { title: '新建策略' } },
    { path: '/tasks/:taskId/edit', component: () => import('./pages/TaskEditorPage.vue'), meta: { title: '编辑策略' } },
    { path: '/tasks/:taskId/results', component: () => import('./pages/StrategyResultsPage.vue'), meta: { title: 'IP 结果' } },
    { path: '/tasks/:taskId', component: () => import('./pages/TaskOverviewPage.vue'), meta: { title: '任务概览' } },
    { path: '/tasks/:taskId/runs', component: () => import('./pages/RunListPage.vue'), meta: { title: '运行记录' } },
    { path: '/tasks/:taskId/recycle-bin', component: () => import('./pages/RunListPage.vue'), meta: { title: '回收站' } },
    { path: '/tasks/:taskId/runs/:runId', component: () => import('./pages/RunDetailPage.vue'), meta: { title: '执行详情' } },
    { path: '/queue', component: () => import('./pages/QueuePage.vue'), meta: { title: '运行队列' } },
    { path: '/connections', component: () => import('./pages/ConnectionsPage.vue'), meta: { title: '服务连接' } },
    { path: '/operation-logs', component: () => import('./pages/LogsPage.vue'), meta: { title: '操作日志' } },
    { path: '/settings', component: () => import('./pages/SettingsPage.vue'), meta: { title: '系统设置' } },
    { path: '/:pathMatch(.*)*', component: () => import('./pages/NotFoundPage.vue'), meta: { title: '页面不存在' } },
  ],
  scrollBehavior(to, from, saved) {
    const entry = window.history.state?.position as number | undefined
    const position = routeScrollPosition(saved, entry === undefined ? undefined : positions.get(entry), to.path === from.path, previousPosition)
    activeEntry = entry
    restoreScrollPosition(position)
    return false
  },
})
router.beforeEach(() => {
  previousPosition = readScrollPosition()
  if (activeEntry !== undefined) positions.set(activeEntry, previousPosition)
  cancelScrollRestore()
})
router.beforeEach(async to => {
  if (!session.ready) { try { await checkSession() } catch { session.ready = true } }
  if (to.path !== '/login' && !session.authenticated) return { path: '/login', query: { return: to.fullPath } }
  if (to.path === '/login' && session.authenticated) return '/tasks'
})
router.afterEach(to => { document.title = String(to.meta.title || '优选管理') + ' · CloudflareST' })
