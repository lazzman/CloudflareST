<script setup lang="ts">
import { ref } from 'vue'
import { loadSettings } from '../lib/settings'
import { useMutation } from '../lib/useMutation'
import { useResource } from '../lib/useResource'
import ResourceState from '../components/ResourceState.vue'
import MutationState from '../components/MutationState.vue'
const timezone = ref('')
const historyDays = ref(30)
const validation = ref('')
const mutation = useMutation()
const resource = useResource(async () => { const value = await loadSettings(); timezone.value = value.timezone || ''; historyDays.value = value.history_days || 30; return value })
async function save() {
  validation.value = ''
  try { new Intl.DateTimeFormat('zh-CN', { timeZone: timezone.value }).format() } catch { validation.value = '请输入有效 IANA 时区，例如 Asia/Shanghai'; return }
  if (!Number.isInteger(historyDays.value) || historyDays.value < 1 || historyDays.value > 3650) { validation.value = '历史保留期应为 1～3650 的整数天数'; return }
  if (await mutation.submit('/settings', { method: 'PATCH', body: { timezone: timezone.value, history_days: historyDays.value } })) void resource.reload()
}
</script>
<template><header class="page-heading"><div><h1 tabindex="-1">系统设置</h1></div></header><ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" /><form v-if="resource.data.value" class="section stack" @submit.prevent="save"><h2>时间与历史</h2><label>统一系统时区<input v-model.trim="timezone" required list="timezones" /><datalist id="timezones"><option value="Asia/Shanghai" /><option value="UTC" /><option value="Asia/Tokyo" /></datalist></label><p class="notice warning">修改时区将重算所有已启用任务的下一次未来触发时点，不更改已有运行时间。</p><label>运行历史保留（天）<input v-model.number="historyDays" type="number" min="1" max="3650" step="1" required /></label><p>运行记录删除后进入 7 天回收站。操作日志固定保留 30 天，与运行历史设置独立。</p><p v-if="validation" class="field-error" role="alert">{{ validation }}</p><button class="primary" :disabled="mutation.busy.value">保存系统设置</button></form><section v-if="resource.data.value" class="section"><h2>环境能力与认证</h2><dl class="facts"><div><dt>ICMP</dt><dd>{{ resource.data.value.icmp?.available === true ? '可用' : resource.data.value.icmp?.available === false ? '不可用（任务中可明确选择 TCPING）' : '能力未知' }} · {{ resource.data.value.icmp?.message || '未提供说明' }}</dd></div><div><dt>密码来源</dt><dd>{{ resource.data.value.password_source || '未知' }}</dd></div></dl><p class="small muted">环境变量优先；未配置时每次启动重新生成。忘记密码请在本机配置并重启，不提供网页找回、回读或展示初始化密码。</p></section></template>
