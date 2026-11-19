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
    <section class="login-box">
      <div class="login-brand"><AppIcon name="activity" :size="18" /><strong>CloudflareST</strong><span>IP 优选管理</span></div>
      <div class="login-intro"><h1 tabindex="-1">密码登录</h1><p class="muted">使用共享管理密码进入优选工作区。</p></div>
      <p v-if="session.notice" class="notice" role="status">{{ session.notice }}<button v-if="session.notice.includes('未确认服务端退出')" type="button" @click="retryLogout">重试退出当前会话</button></p>
      <p v-if="mock" class="notice warning">测试预览：请输入任意非空字符串模拟认证；不会连接后端或外部服务。</p>
      <form class="stack" @submit.prevent="submit"><label for="password">管理密码</label><input id="password" v-model="password" type="password" required autocomplete="current-password" placeholder="请输入管理密码" :aria-invalid="!!error" :aria-describedby="error ? 'login-error' : 'login-help'" /><p v-if="error" id="login-error" class="notice danger" role="alert">{{ error }}</p><button class="primary" :disabled="busy">{{ busy ? '正在登录…' : '登录管理端' }}</button></form>
      <div class="login-help" id="login-help"><AppIcon name="info" :size="15" /><p>环境变量优先；未配置时每次启动重新生成。忘记密码请在本机配置并重启，本页不回读初始密码。</p></div>
      <p class="login-security"><AppIcon name="shield" :size="13" />登录不改变后台任务状态</p>
    </section>
  </div>
</template>
