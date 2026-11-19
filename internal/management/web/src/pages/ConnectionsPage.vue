<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { request, segment } from '../api/client'
import type { Connection, ConnectionKind, Target } from '../api/types'
import { useResource } from '../lib/useResource'
import { useMutation } from '../lib/useMutation'
import { label } from '../lib/domain'
import ResourceState from '../components/ResourceState.vue'
import MutationState from '../components/MutationState.vue'
import ModalDialog from '../components/ModalDialog.vue'
import TargetSelector from '../components/TargetSelector.vue'
import ProtectionControl from '../components/ProtectionControl.vue'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import SurfacePanel from '../components/SurfacePanel.vue'
type ConnectionWithSecrets = Connection & { secrets: Record<string, string> }
const resource = useResource<ConnectionWithSecrets[]>(async () => {
  const connections = await request<Connection[]>('/connections')
  return Promise.all(connections.map(async connection => {
    const details = connection.credentials_set
      ? await request<{ secrets: Record<string, string> }>('/connections/' + segment(connection.id) + '/credentials')
      : { secrets: {} }
    return { ...connection, secrets: details.secrets }
  }))
})
const mutation = useMutation()
const editing = ref<Connection | 'new'>()
const removing = ref<Connection>()
const targets = ref<Target[]>([])
const form = reactive({ kind: 'alidns' as ConnectionKind, name: '', account_id: '', secrets: {} as Record<string, string> })
const credentialLabels: Record<string, string> = { access_key_id: 'AccessKey ID', access_key_secret: 'AccessKey Secret', bot_token: 'Bot Token', chat_id: 'Chat ID', webhook_url: '企业微信 Webhook URL' }
const secretKeys = computed(() => form.kind === 'alidns' ? [{ key: 'access_key_id', label: 'AccessKey ID' }, { key: 'access_key_secret', label: 'AccessKey Secret' }] : form.kind === 'telegram' ? [{ key: 'bot_token', label: 'Bot Token' }, { key: 'chat_id', label: 'Chat ID' }] : [{ key: 'webhook_url', label: '企业微信 Webhook URL' }])
const replacing = ref(true)
function open(connection?: Connection) { form.kind = connection?.kind || 'alidns'; form.name = connection?.name || ''; form.account_id = connection?.account_id || ''; form.secrets = {}; replacing.value = !connection; editing.value = connection || 'new'; mutation.error.value = undefined; mutation.receipt.value = undefined }
function close() { editing.value = undefined; form.secrets = {} }
async function save() {
  if (!editing.value) return
  const item = editing.value === 'new' ? undefined : editing.value
  const result = await mutation.submit(item ? '/connections/' + segment(item.id) : '/connections', { method: item ? 'PATCH' : 'POST', revision: item?.revision, body: { connection: { kind: form.kind, name: form.name, account_id: form.account_id, ...(replacing.value ? { secrets: { ...form.secrets } } : {}) } } })
  form.secrets = {}
  if (result) { close(); void resource.reload() }
}
async function remove() { if (removing.value && await mutation.submit('/connections/' + segment(removing.value.id), { method: 'DELETE' })) { removing.value = undefined; void resource.reload() } }
function referenceId(reference: NonNullable<Connection['referenced_by']>[number]) { return typeof reference === 'string' ? reference : reference.task_id || reference.id || '' }
function credentialEntries(connection: ConnectionWithSecrets) { return Object.entries(connection.secrets).map(([key, value]) => ({ key, label: credentialLabels[key] || key, value })) }
</script>
<template>
  <PageHeader title="服务连接" desc="配置与凭据明文回显；凭据仅保留在本页内存，不进入本地存储。保存不发送测试消息。"><button class="primary" type="button" @click="open()"><AppIcon name="plus" />新增连接</button></PageHeader>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <SurfacePanel title="共享服务连接" :meta="resource.data.value ? resource.data.value.length + ' 项' : '等待读取'" :aria-busy="resource.loading.value || resource.refreshing.value">
    <table v-if="resource.data.value?.length" class="responsive-table"><caption class="sr-only">共享服务连接配置与凭据</caption><thead><tr><th scope="col">连接名称 / 类型</th><th scope="col">账号 / 明文凭据</th><th scope="col">引用任务</th><th scope="col">操作</th></tr></thead><tbody>
      <tr v-for="connection in resource.data.value" :key="connection.id">
        <td data-label="连接名称 / 类型"><strong>{{ connection.name || connection.id }}</strong> · {{ label(connection.kind) }}<code class="block">{{ connection.id }}</code></td>
        <td data-label="账号 / 明文凭据"><span>账号标识：{{ connection.account_id || '未提供' }}</span><template v-if="credentialEntries(connection).length"><div v-for="entry in credentialEntries(connection)" :key="entry.key" class="block"><span class="small muted">{{ entry.label }}</span><code class="block">{{ entry.value }}</code></div></template><span v-else class="block">{{ connection.credentials_set ? '凭据字段未返回' : '凭据未设置' }}</span><span class="block small">版本 {{ connection.revision ?? '未知' }}</span></td>
        <td data-label="引用任务"><ul v-if="connection.referenced_by?.length"><li v-for="reference in connection.referenced_by" :key="referenceId(reference)"><RouterLink :to="'/tasks/' + referenceId(reference)">{{ referenceId(reference) }}</RouterLink></li></ul><span v-else>无任务引用；活动引用仍由服务端核验</span></td>
        <td data-label="操作"><div class="actions"><button type="button" :disabled="connection.revision === undefined" @click="open(connection)"><AppIcon name="edit" :size="16" />编辑 / 替换凭据</button><button type="button" :disabled="!!connection.referenced_by?.length" @click="removing = connection"><AppIcon name="trash" :size="16" />回收连接</button></div><p v-if="connection.referenced_by?.length" class="small muted">先解除任务引用；不提供强制删除或自动改绑。</p></td>
      </tr>
    </tbody></table>
    <p v-else-if="resource.data.value && !resource.error.value" class="empty-state">还没有连接。新增连接不会发起测速、DNS 更新或通知。</p>
  </SurfacePanel>
  <section class="section"><h2>只读发现 DNS 目标与保护状态</h2><TargetSelector v-model="targets" /><div v-for="target in targets" :key="target.target_id || target.record_id"><ProtectionControl :target="target" @changed="resource.reload" /></div></section>
  <ModalDialog v-if="editing" :title="editing === 'new' ? '新增共享连接' : '编辑连接 / 替换凭据'" @close="close"><form class="stack" @submit.prevent="save"><label>类型<select v-model="form.kind" :disabled="editing !== 'new'" @change="form.secrets = {}"><option value="alidns">阿里云 DNS</option><option value="telegram">Telegram</option><option value="wecom">企业微信</option></select></label><label>连接名称<input v-model.trim="form.name" required maxlength="100" /></label><label>账号非敏感标识<input v-model.trim="form.account_id" autocomplete="off" /></label><label v-if="editing !== 'new'" class="check-line"><input v-model="replacing" type="checkbox" @change="form.secrets = {}" />明确替换凭据（输入为空，不回填旧值）</label><fieldset v-if="replacing"><legend>新凭据（明文显示）</legend><label v-for="field in secretKeys" :key="field.key">{{ field.label }}<input v-model="form.secrets[field.key]" type="text" required autocomplete="off" autocapitalize="none" spellcheck="false" /></label></fieldset><p class="notice warning">活动执行引用会阻止替换或回收。冲突时不覆盖，凭据在每次提交后清空；如需再次提交请重新输入并核对最新版本。</p><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" /><div class="actions"><button type="button" class="safe-focus" @click="close">取消</button><button class="primary" :disabled="mutation.busy.value">保存连接</button></div></form></ModalDialog>
  <ModalDialog v-if="removing" title="回收共享连接" @close="removing = undefined"><p>{{ removing.name || removing.id }} · <code>{{ removing.id }}</code></p><p>连接将被删除；须先解除任务及活动引用。本操作不会修改 DNS 或撤回消息。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="removing = undefined">取消</button><button class="danger-button" :disabled="mutation.busy.value" @click="remove">确认回收</button></div></ModalDialog>
</template>
