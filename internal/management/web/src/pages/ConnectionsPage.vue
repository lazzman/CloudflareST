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
const resource = useResource(() => request<Connection[]>('/connections'))
const mutation = useMutation()
const editing = ref<Connection | 'new'>()
const removing = ref<Connection>()
const targets = ref<Target[]>([])
const form = reactive({ kind: 'alidns' as ConnectionKind, name: '', account_id: '', secrets: {} as Record<string, string> })
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
</script>
<template>
  <header class="page-heading"><div><p class="eyebrow">共享引用 · 凭据不回读</p><h1 tabindex="-1">服务连接</h1><p class="muted">凭据只在提交时使用，不进入本地存储。保存不发送测试消息。</p></div><button class="primary" @click="open()">新增连接</button></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <table v-if="resource.data.value?.length" class="responsive-table"><caption class="sr-only">共享服务连接</caption><thead><tr><th>连接名称 / 类型</th><th>账号 / 凭据状态</th><th>引用任务</th><th>操作</th></tr></thead><tbody><tr v-for="connection in resource.data.value" :key="connection.id"><td data-label="名称 / 类型"><strong>{{ connection.name || connection.id }}</strong> · {{ label(connection.kind) }}<code class="block">{{ connection.id }}</code></td><td data-label="账号 / 凭据">{{ connection.account_id || '未提供' }}<span class="block">{{ connection.credentials_set ? '凭据已设置（不可回读）' : '凭据未设置' }} · 版本 {{ connection.revision ?? '未知' }}</span></td><td data-label="引用任务"><ul><li v-for="reference in connection.referenced_by || []" :key="referenceId(reference)"><RouterLink :to="'/tasks/' + referenceId(reference)">{{ referenceId(reference) }}</RouterLink></li></ul><span v-if="!connection.referenced_by?.length">无任务引用；活动引用仍由服务端核验</span></td><td data-label="操作"><div class="actions"><button :disabled="connection.revision === undefined" @click="open(connection)">编辑 / 替换凭据</button><button :disabled="!!connection.referenced_by?.length" @click="removing = connection">回收连接</button></div><p v-if="connection.referenced_by?.length" class="small muted">先解除任务引用；不提供强制删除或自动改绑。</p></td></tr></tbody></table>
  <p v-else-if="resource.data.value && !resource.error.value" class="empty-state">还没有连接。新增连接不会发起测速、DNS 更新或通知。</p>
  <section class="section"><h2>只读发现 DNS 目标与保护状态</h2><TargetSelector v-model="targets" /><div v-for="target in targets" :key="target.target_id || target.record_id"><ProtectionControl :target="target" @changed="resource.reload" /></div></section>
  <ModalDialog v-if="editing" :title="editing === 'new' ? '新增共享连接' : '编辑连接 / 替换凭据'" @close="close"><form class="stack" @submit.prevent="save"><label>类型<select v-model="form.kind" :disabled="editing !== 'new'" @change="form.secrets = {}"><option value="alidns">阿里云 DNS</option><option value="telegram">Telegram</option><option value="wecom">企业微信</option></select></label><label>连接名称<input v-model.trim="form.name" required maxlength="100" /></label><label>账号非敏感标识<input v-model.trim="form.account_id" autocomplete="off" /></label><label v-if="editing !== 'new'" class="check-line"><input v-model="replacing" type="checkbox" @change="form.secrets = {}" />明确替换凭据（输入为空，不回填旧值）</label><fieldset v-if="replacing"><legend>新凭据（只写）</legend><label v-for="field in secretKeys" :key="field.key">{{ field.label }}<input v-model="form.secrets[field.key]" type="password" required autocomplete="new-password" /></label></fieldset><p class="notice warning">活动执行引用会阻止替换或回收。冲突时不覆盖，凭据在每次提交后清空；如需再次提交请重新输入并核对最新版本。</p><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" /><div class="actions"><button type="button" class="safe-focus" @click="close">取消</button><button class="primary" :disabled="mutation.busy.value">保存连接</button></div></form></ModalDialog>
  <ModalDialog v-if="removing" title="回收共享连接" @close="removing = undefined"><p>{{ removing.name || removing.id }} · <code>{{ removing.id }}</code></p><p>连接将被删除；须先解除任务及活动引用。本操作不会修改 DNS 或撤回消息。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="removing = undefined">取消</button><button class="danger-button" :disabled="mutation.busy.value" @click="remove">确认回收</button></div></ModalDialog>
</template>
