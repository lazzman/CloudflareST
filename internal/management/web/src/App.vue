<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { session, logout } from './lib/session'
import { request } from './api/client'
import type { Queue } from './api/types'
import { clearSettings, loadSettings, systemTimezone } from './lib/settings'
import { useResource } from './lib/useResource'
import { label } from './lib/domain'
import { setTheme, themeMode } from './lib/appearance'
import AppIcon from './components/AppIcon.vue'
const route = useRoute()
const router = useRouter()
const menu = ref(false)
const collapsed = ref(false)
const menuButton = ref<HTMLButtonElement>()
function closeMenu() { menu.value = false; menuButton.value?.focus() }
const leaving = ref(false)
const mock = __MOCK_API__
const primaryLinks = [{ path: '/tasks', text: '优选策略', icon: 'dashboard' }]
const adminLinks = [{ path: '/queue', text: '运行队列', icon: 'activity' }, { path: '/connections', text: '服务连接', icon: 'layers' }, { path: '/operation-logs', text: '操作日志', icon: 'log' }, { path: '/settings', text: '系统设置', icon: 'sliders' }]
const section = computed(() => [...primaryLinks, ...adminLinks].find(link => route.path.startsWith(link.path)))
const global = useResource(async () => {
  if (!session.authenticated) { clearSettings(); return undefined }
  const [queue] = await Promise.all([request<Queue>('/queue'), loadSettings()])
  return { queue }
}, { key: () => session.authenticated, poll: () => session.authenticated })
watch(() => route.path, async () => { menu.value = false; await nextTick(); document.querySelector<HTMLElement>('h1')?.focus({ preventScroll: true }) })
async function signOut() {
  leaving.value = true
  try { await logout() } catch {} finally { leaving.value = false; global.data.value = undefined; await router.replace('/login') }
}
</script>
<template>
  <a class="skip-link" href="#main-content" @click.prevent="($el.ownerDocument.getElementById('main-content') as HTMLElement)?.focus()">跳到主要内容</a>
  <div v-if="session.authenticated" class="shell" :class="{ 'rail-collapsed': collapsed }">
    <aside class="sidebar m-rail" :class="{ expanded: menu }" @keydown.esc="closeMenu">
      <div class="brand"><span class="brand-mark" aria-hidden="true"><AppIcon name="activity" :size="20" /></span><div class="brand-text"><strong>CloudflareST</strong><span>IP 优选管理</span></div></div>
      <button ref="menuButton" class="mobile-only icon-button mobile-menu" type="button" :aria-expanded="menu" aria-controls="primary-nav" @click="menu = !menu"><AppIcon :name="menu ? 'x' : 'menu'" /><span class="sr-only">{{ menu ? '收起导航' : '打开导航' }}</span></button>
      <nav id="primary-nav" aria-label="主导航"><RouterLink v-for="link in primaryLinks" :key="link.path" :to="link.path" :class="{selected: route.path.startsWith(link.path)}" :title="collapsed ? link.text : undefined"><AppIcon :name="link.icon" /><span class="nav-label">{{ link.text }}</span></RouterLink><div class="nav-divider" role="presentation"></div><RouterLink v-for="link in adminLinks" :key="link.path" :to="link.path" :class="{selected: route.path.startsWith(link.path)}" :title="collapsed ? link.text : undefined"><AppIcon :name="link.icon" /><span class="nav-label">{{ link.text }}</span></RouterLink></nav>
      <div class="rail-foot"><div class="identity"><div class="identity-copy"><strong>共享管理空间</strong><span>单实例 · 数据共享</span></div></div><button class="logout" type="button" :disabled="leaving" @click="signOut"><AppIcon name="logout" /><span class="nav-label">{{ leaving ? '退出中…' : '退出登录' }}</span></button></div>
    </aside>
    <div class="workspace">
      <header class="topbar"><div class="breadcrumb"><button class="icon-button desktop-only" :aria-label="collapsed ? '展开侧栏' : '收起侧栏'" :aria-expanded="!collapsed" @click="collapsed = !collapsed"><AppIcon name="sidebar" /></button><strong>{{ route.meta.title || section?.text }}</strong></div><div class="topbar-tools"><label class="theme-control"><AppIcon :name="themeMode === 'dark' ? 'moon' : 'sun'" :size="16" /><span class="sr-only">外观模式</span><select aria-label="外观模式" :value="themeMode" @change="setTheme(($event.target as HTMLSelectElement).value)"><option value="system">跟随系统</option><option value="light">浅色</option><option value="dark">深色</option></select></label></div></header>
      <div class="global-status"><span class="status-dot" :class="{ busy: global.data.value?.queue.current }" aria-hidden="true" /><template v-if="global.data.value"><RouterLink v-if="global.data.value.queue.current" :to="'/tasks/' + global.data.value.queue.current.task_id + '/runs/' + global.data.value.queue.current.id">{{ global.data.value.queue.current.task_name || global.data.value.queue.current.task_id }} · {{ label(global.data.value.queue.current.stage) }}</RouterLink><span v-else>当前席位空闲</span><RouterLink to="/queue">等待 {{ global.data.value.queue.waiting?.length ?? 0 }}</RouterLink></template><span v-else>全局状态{{ global.error.value ? '读取失败' : '读取中' }}</span><span class="status-timezone"><AppIcon name="clock" :size="14" />{{ systemTimezone || '时区未知' }}</span></div>
      <p v-if="global.error.value" class="notice warning compact">全局数据更新中断，显示上次状态。<button @click="global.reload">重新读取</button></p>
      <p v-if="mock" class="mock-banner">本地测试预览 · 所有接口为内存 mock，刷新重置；未连接任何真实外部服务</p>
      <main id="main-content" tabindex="-1"><RouterView :key="route.path + ':' + session.generation" /></main>
      <footer class="workspace-footer"><span>CloudflareST · 优选管理</span><span>优选、DNS 与通知分别记录</span></footer>
    </div>
  </div>
  <main v-else id="main-content" class="login-layout" tabindex="-1"><RouterView :key="'public:' + session.generation" /></main>
</template>
