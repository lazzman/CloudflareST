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
const httpTransport = window.location.protocol === 'http:'
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
const editingConnection = computed(() => editing.value && editing.value !== 'new' ? editing.value : undefined)
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
function shortId(id: string) { return id.length > 14 ? id.slice(0, 8) + '…' : id }
function referenceName(reference: NonNullable<Connection['referenced_by']>[number]) { return typeof reference === 'string' ? shortId(reference) : reference.name || shortId(reference.task_id || reference.id || '') }
function connectionType(connection: Connection) { const type = label(connection.kind); return (connection.name || '').replace(/\s+/g, '').includes(type.replace(/\s+/g, '')) ? '' : type }
function credentialEntries(connection: ConnectionWithSecrets) { return Object.entries(connection.secrets).map(([key, value]) => ({ key, label: credentialLabels[key] || key, value })) }
</script>
<template>
  <PageHeader title="服务连接" desc="管理 DNS 与通知服务的连接。保存不会发送测试消息。"><button class="primary" type="button" @click="open()"><AppIcon name="plus" />新增连接</button></PageHeader>
  <p v-if="httpTransport" class="notice warning compact">当前使用 HTTP，密码与凭据传输未加密；仅建议在可信网络使用。</p>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <SurfacePanel title="已保存的连接" :meta="resource.data.value ? resource.data.value.length + ' 个连接' : '等待读取'" :aria-busy="resource.loading.value || resource.refreshing.value">
    <table v-if="resource.data.value?.length" class="responsive-table connections-table"><caption class="sr-only">共享服务连接配置与凭据</caption><thead><tr><th scope="col">连接</th><th scope="col">账号与凭据</th><th scope="col">引用</th><th scope="col">操作</th></tr></thead><tbody>
      <tr v-for="connection in resource.data.value" :key="connection.id">
        <td data-label="连接"><strong class="connection-name">{{ connection.name || connection.id }}</strong><p v-if="connectionType(connection)" class="connection-meta">{{ connectionType(connection) }}</p></td>
        <td data-label="账号与凭据"><p class="connection-account">账号：{{ connection.account_id || '未提供' }}</p><p class="connection-meta">{{ connection.credentials_set ? '凭据已保存' : '凭据未设置' }} · 版本 {{ connection.revision ?? '未知' }}</p><details v-if="credentialEntries(connection).length" class="credential-details"><summary>查看凭据</summary><dl class="credential-list"><div v-for="entry in credentialEntries(connection)" :key="entry.key"><dt>{{ entry.label }}</dt><dd><code>{{ entry.value }}</code></dd></div></dl></details><p v-else-if="connection.credentials_set" class="small muted">凭据字段未返回。</p></td>
        <td data-label="引用"><template v-if="connection.referenced_by?.length"><p class="reference-count">{{ connection.referenced_by.length }} 个任务引用</p><ul class="reference-list"><li v-for="reference in connection.referenced_by" :key="referenceId(reference)"><RouterLink :to="'/tasks/' + referenceId(reference)" :title="referenceId(reference)">{{ referenceName(reference) }}</RouterLink></li></ul></template><span v-else class="muted">无引用</span></td>
        <td data-label="操作"><div class="actions connection-actions"><button type="button" :disabled="connection.revision === undefined" @click="open(connection)"><AppIcon name="edit" :size="15" />编辑</button><button type="button" class="danger-link" :disabled="!!connection.referenced_by?.length" :title="connection.referenced_by?.length ? '有任务引用，暂不能删除' : undefined" @click="removing = connection"><AppIcon name="trash" :size="15" />删除</button></div></td>
      </tr>
    </tbody></table>
    <p v-else-if="resource.data.value && !resource.error.value" class="empty-state">还没有连接。添加一个 DNS 或通知服务，任务才能推送结果。</p>
    <p v-if="resource.data.value?.length" class="panel-foot">凭据默认收起，展开后明文显示；不写入本地存储。</p>
  </SurfacePanel>
  <details class="advanced-panel">
    <summary>高级：查看 DNS 目标与保护状态</summary>
    <div class="advanced-panel-body"><TargetSelector v-model="targets" /><div v-for="target in targets" :key="target.target_id || target.record_id"><ProtectionControl :target="target" @changed="resource.reload" /></div></div>
  </details>
  <ModalDialog v-if="editing" :title="editing === 'new' ? '新增连接' : '编辑连接'" size="narrow" @close="close"><form class="stack connection-form" @submit.prevent="save"><label>服务类型<select v-model="form.kind" :disabled="editing !== 'new'" @change="form.secrets = {}"><option value="alidns">阿里云 DNS</option><option value="telegram">Telegram</option><option value="wecom">企业微信</option></select></label><label>连接名称<input v-model.trim="form.name" required maxlength="100" /></label><label>账号（可选）<input v-model.trim="form.account_id" autocomplete="off" /></label><template v-if="editing === 'new'"><fieldset><legend>凭据</legend><label v-for="field in secretKeys" :key="field.key">{{ field.label }}<input v-model="form.secrets[field.key]" type="text" required autocomplete="off" autocapitalize="none" spellcheck="false" /></label><p class="small muted">凭据不写入本地存储。</p></fieldset></template><template v-else><p class="credential-summary"><span>{{ editingConnection?.credentials_set ? '凭据已保存' : '凭据未设置' }}</span><span class="muted">版本 {{ editingConnection?.revision ?? '未知' }}</span></p><p v-if="editingConnection" class="connection-id">连接 ID <code>{{ editingConnection.id }}</code></p><label class="check-line"><input v-model="replacing" type="checkbox" @change="form.secrets = {}" />更新凭据</label><p class="small muted">不勾选则只修改名称和账号；勾选后需重新填写，旧凭据不会回填。</p><fieldset v-if="replacing"><legend>新凭据</legend><label v-for="field in secretKeys" :key="field.key">{{ field.label }}<input v-model="form.secrets[field.key]" type="text" required autocomplete="off" autocapitalize="none" spellcheck="false" /></label><p class="small muted">保存后将替换旧凭据。</p></fieldset></template><p v-if="editingConnection?.referenced_by?.length" class="notice warning">该连接被 {{ editingConnection.referenced_by.length }} 个任务引用；替换凭据前请确认影响范围。</p><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" /><div class="actions form-actions"><button type="button" class="safe-focus" @click="close">取消</button><button class="primary" :disabled="mutation.busy.value">保存连接</button></div></form></ModalDialog>
  <ModalDialog v-if="removing" title="删除连接" size="narrow" @close="removing = undefined"><p>{{ removing.name || removing.id }}</p><p>删除后无法恢复；有任务引用时需先解除引用。不会修改 DNS 或撤回消息。</p><MutationState :error="mutation.error.value" /><div class="actions form-actions"><button class="safe-focus" @click="removing = undefined">取消</button><button class="danger-button" :disabled="mutation.busy.value" @click="remove">确认删除</button></div></ModalDialog>
</template>
