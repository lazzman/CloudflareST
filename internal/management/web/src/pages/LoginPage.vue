<script setup lang="ts">
import { onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { login, logout, session } from '../lib/session'
import AppIcon from '../components/AppIcon.vue'
const password = ref('')
const busy = ref(false)
const error = ref('')
const route = useRoute()
const router = useRouter()
const mock = __MOCK_API__
onUnmounted(() => { password.value = '' })
async function submit() {
  busy.value = true; error.value = ''
  try {
    await login(password.value)
    const destination = typeof route.query.return === 'string' ? route.query.return : '/tasks'
    await router.replace(destination.startsWith('/') && !destination.startsWith('//') && !destination.startsWith('/login') ? destination : '/tasks')
  } catch (caught) { error.value = caught instanceof Error ? caught.message : '登录失败' }
  finally { password.value = ''; busy.value = false }
}
async function retryLogout() { try { await logout() } catch (caught) { error.value = (caught as Error).message } }
</script>
<template>
  <div class="login-frame">
    <section class="login-story" aria-label="IP优选管理介绍"><div class="brand"><span class="brand-mark"><AppIcon name="activity" :size="24" /></span><div class="brand-text"><strong>CloudflareST</strong><span>IP 优选管理</span></div></div><div class="login-story-copy"><p class="story-label">候选 · 测量 · 优选 · 投递</p><h2>找到更好的连接，<br />每一步都有依据。</h2><p>把分散的来源、测速规则和推送目标，<br class="desktop-only" />放进一个可追溯的管理工作区。</p></div><figure class="connection-illustration"><svg viewBox="0 0 460 210" role="img" aria-label="多个候选来源经优选后连接到明确目标的流程示意"><g fill="none" stroke="currentColor" stroke-width="1.3"><path class="diagram-track" d="M60 45H110Q130 45 130 65V95Q130 105 150 105H190M60 105H190M60 165H110Q130 165 130 145V115Q130 105 150 105M270 105H310Q330 105 330 85V60H395M270 105H395M270 105H310Q330 105 330 125V150H395"/><rect x="190" y="65" width="80" height="80" rx="22" class="diagram-core"/><circle cx="60" cy="45" r="14"/><circle cx="60" cy="105" r="14"/><circle cx="60" cy="165" r="14"/><circle cx="400" cy="60" r="10"/><circle cx="400" cy="105" r="10"/><circle cx="400" cy="150" r="10"/><path class="diagram-pulse" d="M205 105h12l7-17 13 34 7-17h12"/></g></svg><figcaption>流程示意 · 不代表实时网络状态</figcaption></figure><div class="login-capabilities"><span><AppIcon name="shield" :size="16" />共享密码认证</span><span><AppIcon name="clock" :size="16" />手动与定时</span><span><AppIcon name="log" :size="16" />完整运行记录</span></div></section>
    <section class="login-box"><div class="login-intro"><span class="login-lock"><AppIcon name="lock" :size="24" /></span><h1 tabindex="-1">密码登录</h1><p class="muted">使用共享管理密码，进入你的优选工作区。</p></div>
      <p v-if="session.notice" class="notice" role="status">{{ session.notice }}<button v-if="session.notice.includes('未确认服务端退出')" type="button" @click="retryLogout">重试退出当前会话</button></p>
      <p v-if="mock" class="notice warning">测试预览：请输入任意非空字符串模拟认证；不会连接后端或外部服务。</p>
      <form class="stack" @submit.prevent="submit"><label for="password">管理密码</label><input id="password" v-model="password" type="password" required autocomplete="current-password" placeholder="请输入管理密码" :aria-invalid="!!error" :aria-describedby="error ? 'login-error' : 'login-help'" /><p v-if="error" id="login-error" class="notice danger" role="alert">{{ error }}</p><button class="primary" :disabled="busy">{{ busy ? '正在登录…' : '登录管理端' }}<AppIcon name="arrowRight" :size="18" /></button></form>
      <div class="login-help" id="login-help"><AppIcon name="info" :size="16" /><p>环境变量优先；未配置时每次启动重新生成。忘记密码请在本机配置并重启，本页不回读初始密码。</p></div><p class="login-security"><AppIcon name="shield" :size="14" />登录不改变后台任务状态</p>
    </section>
  </div>
</template>
