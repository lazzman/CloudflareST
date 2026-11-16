<script setup lang="ts">
import { computed, ref } from 'vue'
import { request, segment, query } from '../api/client'
import type { Connection, Target } from '../api/types'
import { targetKey, targetName } from '../lib/domain'
import { useResource } from '../lib/useResource'
import { cloneDto } from '../lib/serialize'
import ResourceState from './ResourceState.vue'
import TargetFacts from './TargetFacts.vue'
const props = defineProps<{ modelValue: Target[] }>()
const emit = defineEmits<{ 'update:modelValue': [Target[]] }>()
const connectionId = ref('')
const domain = ref('')
const connections = useResource(() => request<Connection[]>('/connections'))
const dnsConnections = computed(() => connections.data.value?.filter(connection => connection.kind === 'alidns' && connection.credentials_set) || [])
const domains = useResource(async () => {
  domain.value = ''
  return connectionId.value ? request<string[]>('/connections/' + segment(connectionId.value) + '/domains') : []
}, { key: () => connectionId.value })
const records = useResource(() => domain.value && connectionId.value ? request<Target[]>('/connections/' + segment(connectionId.value) + '/dns-records' + query({ domain: domain.value })) : Promise.resolve([]), { key: () => connectionId.value + '/' + domain.value })
const available = computed(() => records.data.value?.filter(target => ['A', 'AAAA'].includes(target.type || '')) || [])
function add(target: Target) {
  if (!props.modelValue.some(selected => targetKey(selected) === targetKey(target))) emit('update:modelValue', [...props.modelValue, cloneDto(target)])
}
</script>
<template>
  <div class="stack">
    <p class="small muted">只选择现有 A／AAAA 记录；不新建记录，不改 TTL、线路或其他记录。AAAA 不会自动启用任务 IPv6。</p>
    <ResourceState :loading="connections.loading.value" :error="connections.error.value" @retry="connections.reload" />
    <div class="form-grid"><label>阿里云连接<select v-model="connectionId"><option value="">选择已设置凭据的连接</option><option v-for="connection in dnsConnections" :key="connection.id" :value="connection.id">{{ connection.name || connection.id }} / {{ connection.account_id || '未知账号' }}</option></select></label><label>现有域名<select v-model="domain" :disabled="!connectionId || domains.loading.value"><option value="">选择域名</option><option v-for="item in domains.data.value" :key="item" :value="item">{{ item }}</option></select></label></div>
    <ResourceState :loading="domains.loading.value || records.loading.value" :error="domains.error.value || records.error.value" @retry="domains.error.value ? domains.reload() : records.reload()" />
    <p v-if="connections.data.value && !dnsConnections.length"><RouterLink to="/connections">先新增或配置阿里云连接</RouterLink>。不会复活无效历史引用。</p>
    <ul class="plain-list"><li v-for="target in available" :key="targetKey(target)" class="record-choice"><span>{{ targetName(target) }}<br /><code>{{ target.value || '未知 IP' }}</code></span><button type="button" :disabled="modelValue.some(item => targetKey(item) === targetKey(target))" @click="add(target)">{{ modelValue.some(item => targetKey(item) === targetKey(target)) ? '已选择' : '选择记录' }}</button></li></ul>
    <p v-if="domain && records.data.value && !available.length && !records.loading.value" class="muted">当前域名没有可选择的 A／AAAA 记录。</p>
    <section v-for="target in modelValue" :key="targetKey(target)" class="target-entry"><TargetFacts :target="target" /><button type="button" @click="emit('update:modelValue', modelValue.filter(item => targetKey(item) !== targetKey(target)))">移除此选择</button></section>
  </div>
</template>
