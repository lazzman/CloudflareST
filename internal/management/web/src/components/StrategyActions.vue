<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import type { Run, Task } from '../api/types'
import { segment } from '../api/client'
import { useMutation } from '../lib/useMutation'
import MutationState from './MutationState.vue'
import ModalDialog from './ModalDialog.vue'
const props = defineProps<{ task: Task; currentRun?: Run }>()
const emit = defineEmits<{ changed: [] }>()
const router = useRouter()
const mutation = useMutation()
const confirmation = ref(false)
const path = () => '/tasks/' + segment(props.task.id)
async function runOnce() {
  const receipt = await mutation.submit(path() + '/runs')
  if (receipt?.object_id) await router.push({path: path() + '/results', query: {run: receipt.object_id}})
}
async function setSchedule() {
  if (await mutation.submit(path() + '/schedule-state', {body: {enabled: !props.task.schedule?.enabled, revision: props.task.revision}})) emit('changed')
}
async function archive() {
  if (await mutation.submit(path() + (props.task.archived ? '/restore' : '/archive'), {body: {revision: props.task.revision}})) { confirmation.value = false; emit('changed') }
}
</script>
<template>
  <div class="strategy-actions actions">
    <RouterLink :to="path() + '/results'" class="button primary">查看结果</RouterLink>
    <button v-if="!task.archived" :disabled="!!currentRun || mutation.busy.value" @click="runOnce">{{ currentRun ? currentRun.status === 'queued' ? '排队中' : '运行中' : '运行一次' }}</button>
    <RouterLink v-if="!task.archived" :to="path() + '/edit'" class="button">编辑</RouterLink>
  </div>
  <div class="strategy-secondary">
    <button v-if="!task.archived" role="switch" :aria-checked="!!task.schedule?.enabled" :aria-label="(task.schedule?.enabled ? '关闭定时：' : '开启定时：') + (task.name || task.id)" :disabled="mutation.busy.value" class="schedule-switch" @click="setSchedule"><span class="switch-track" aria-hidden="true" />定时{{ task.schedule?.enabled ? '已开启' : '已关闭' }}</button>
    <RouterLink :to="path()" class="button strategy-link">高级管理</RouterLink>
    <button type="button" class="strategy-link" :disabled="!!currentRun || mutation.busy.value" @click="confirmation = true">{{ task.archived ? '恢复策略' : '归档策略' }}</button>
  </div>
  <MutationState v-if="mutation.error.value" :error="mutation.error.value" />
  <ModalDialog v-if="confirmation" :title="task.archived ? '恢复策略' : '归档策略'" @close="confirmation = false"><p>{{ task.name || task.id }}</p><p>{{ task.archived ? '恢复配置和历史，定时保持关闭，不会立即运行或推送。' : '归档不删除历史，也不撤销已有DNS或消息；归档后不接受新运行。' }}</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="confirmation = false">取消</button><button :disabled="mutation.busy.value" @click="archive">确认</button></div></ModalDialog>
</template>
