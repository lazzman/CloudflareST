<script setup lang="ts">
import { ref, shallowRef } from 'vue'
import { request, query, segment } from '../api/client'
import type { Target } from '../api/types'
import { targetKey } from '../lib/domain'
import { useMutation } from '../lib/useMutation'
import { cloneDto } from '../lib/serialize'
import ModalDialog from './ModalDialog.vue'
import TargetFacts from './TargetFacts.vue'
import MutationState from './MutationState.vue'
import PendingOperationNotice from './PendingOperationNotice.vue'
const props = defineProps<{ target: Target }>()
const emit = defineEmits<{ changed: [] }>()
const open = ref(false)
const acknowledged = ref(false)
const current = shallowRef<Target>()
const readAt = ref('')
const readError = ref('')
const reading = ref(false)
const mutation = useMutation()
async function refresh() {
  reading.value = true; acknowledged.value = false; readError.value = ''
  try {
    const records = await request<Target[]>('/connections/' + segment(props.target.connection_id) + '/dns-records' + query({ domain: props.target.domain }))
    const found = records.find(target => targetKey(target) === targetKey(props.target))
    if (!found) throw new Error('当前未找到原记录，不能替换为另一目标')
    current.value = found; readAt.value = new Date().toISOString()
  } catch (caught) { readError.value = (caught as Error).message }
  finally { reading.value = false }
}
function show() { current.value = cloneDto(props.target); open.value = true; acknowledged.value = false; void refresh() }
async function clear() {
  if (!acknowledged.value || !current.value?.target_id || current.value.protection?.revision === undefined) return
  const receipt = await mutation.submit('/dns-targets/' + segment(current.value.target_id) + '/auto-write-protection/clear', { body: { revision: current.value.protection.revision, risk_acknowledged: true } })
  if (receipt) { acknowledged.value = false; emit('changed'); await refresh() }
}
</script>
<template>
  <button v-if="target.protection?.active" type="button" @click="show">解除自动写入暂停</button>
  <ModalDialog v-if="open && current" title="C-02：复核后解除目标自动写入暂停" @close="open = false">
    <TargetFacts :target="current" :read-at="readAt" />
    <PendingOperationNotice v-if="current.target_id" :scope="'/api/v1/dns-targets/' + current.target_id" label="此 DNS 目标" @resolved="emit('changed')" />
    <button type="button" :disabled="reading || mutation.busy.value" @click="refresh">{{ reading ? '读取中…' : '只读核对最新记录' }}</button>
    <p v-if="readError" class="notice warning" role="alert">当前值不可读取：{{ readError }}。不代表旧写请求已停止。</p>
    <p class="notice danger">旧远端请求仍可能迟到并覆盖新值。解除仅恢复这个实际目标的后续自动写入资格，不更新 IP、不补发、不重跑、不改任务开关，不改写原未确认记录；所有共享引用任务均受影响。</p>
    <p v-if="current.protection?.revision === undefined || !current.target_id" class="notice danger">缺少目标身份或保护版本，不能安全解除。请重新读取。</p>
    <label class="check-line"><input v-model="acknowledged" type="checkbox" />我已核对原尝试、当前值及迟到覆盖风险，明确同意解除该目标暂停</label>
    <MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
    <p v-if="mutation.error.value?.details.code === 'TARGET_PROTECTION_CHANGED'">保护已变化。请重新读取最新事实并再次勾选，不会自动清除新保护。</p>
    <div class="actions"><button class="safe-focus" type="button" @click="open = false">保留暂停并关闭</button><button class="danger-button" type="button" :disabled="!acknowledged || reading || mutation.busy.value || !current.protection?.active || current.protection.revision === undefined || !current.target_id" @click="clear">确认解除后续自动写入暂停</button></div>
  </ModalDialog>
</template>
