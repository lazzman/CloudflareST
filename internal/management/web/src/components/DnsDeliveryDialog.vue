<script setup lang="ts">
import { computed, ref } from 'vue'
import { segment } from '../api/client'
import type { Receipt, Run, Target } from '../api/types'
import { targetKey } from '../lib/domain'
import { date } from '../lib/settings'
import { useMutation } from '../lib/useMutation'
import CandidatePicker from './CandidatePicker.vue'
import ModalDialog from './ModalDialog.vue'
import MutationState from './MutationState.vue'
import TargetFacts from './TargetFacts.vue'
import TargetSelector from './TargetSelector.vue'
const props = defineProps<{ taskId: string; runId: string; run?: Run }>()
const emit = defineEmits<{ close: []; done: [Receipt] }>()
const mutation = useMutation()
const targetSelection = ref<Target[]>([])
const selectedIps = ref<Record<string, string>>({})
const acknowledged = ref(false)
const ready = computed(() => targetSelection.value.length > 0 && targetSelection.value.every(target => !!selectedIps.value[targetKey(target)]))
function isNewerWrite(target: Target) {
  const previous = Date.parse(target.last_known_write?.written_at || '')
  const source = Date.parse(props.run?.queued_at || '')
  return Number.isFinite(previous) && Number.isFinite(source) && previous > source
}
function close() { if (!mutation.busy.value) emit('close') }
async function submit() {
  if (!acknowledged.value || !ready.value || mutation.busy.value) return
  const receipt = await mutation.submit('/runs/' + segment(props.runId) + '/dns-deliveries', {
    body: { items: targetSelection.value.map(target => ({ target, ip: selectedIps.value[targetKey(target)] })), risk_acknowledged: true },
  })
  if (receipt) emit('done', receipt)
}
</script>
<template>
  <ModalDialog title="从本次结果推送至现有 DNS 记录" @close="close">
    <p>来源执行：<code>{{ runId }}</code> · 时间 {{ date(run?.started_at || run?.queued_at) }}。仅更换记录 IP，不创建记录、不修改 TTL、线路或其他目标。</p>
    <TargetSelector v-model="targetSelection" />
    <article v-for="target in targetSelection" :key="targetKey(target)" class="target-entry">
      <TargetFacts :target="target" />
      <CandidatePicker v-model="selectedIps[targetKey(target)]" :task-id="taskId" :run-id="runId" :record-type="target.type" />
      <p v-if="isNewerWrite(target)" class="notice warning">较旧的来源运行可能覆盖之后的已确认写入。请复核目标、最近写入记录和实际当前值。</p>
      <p v-if="target.protection?.active" class="notice warning">此目标自动写入已暂停，待人工处理。手动投递不解除暂停。</p>
    </article>
    <label class="check-line"><input v-model="acknowledged" type="checkbox" />我已逐条核对账号、记录身份、读取值、最近写入记录、IP 族及覆盖风险，同意只更新所选现有目标</label>
    <p class="notice warning">提交前生成固定操作号；断线时系统自动查询原号结果，仍未确认时沿用原号，不换号盲重试。解除保护是独立操作。</p>
    <MutationState :error="mutation.error.value" />
    <div class="actions">
      <button class="safe-focus" type="button" :disabled="mutation.busy.value" @click="close">取消</button>
      <button class="primary" type="button" :disabled="!acknowledged || !ready || mutation.busy.value" @click="submit">确认提交已复核目标</button>
    </div>
  </ModalDialog>
</template>
