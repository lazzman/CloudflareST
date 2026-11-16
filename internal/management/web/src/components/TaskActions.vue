<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { segment } from '../api/client'
import type { Run, Task } from '../api/types'
import { useMutation } from '../lib/useMutation'
import ModalDialog from './ModalDialog.vue'
import MutationState from './MutationState.vue'
const props = defineProps<{ task: Task; currentRun?: Run | null; compact?: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const router = useRouter()
const mutation = useMutation()
const confirm = ref<'archive' | 'restore'>()
async function run() {
  const receipt = await mutation.submit('/tasks/' + segment(props.task.id) + '/runs')
  if (receipt?.object_id) await router.push('/tasks/' + props.task.id + '/runs/' + receipt.object_id)
  else if (receipt) emit('changed')
}
async function toggleSchedule() {
  if (await mutation.submit('/tasks/' + segment(props.task.id) + '/schedule-state', { body: { enabled: !props.task.schedule?.enabled } })) emit('changed')
}
async function archiveRestore() {
  const action = confirm.value
  if (!action) return
  if (await mutation.submit('/tasks/' + segment(props.task.id) + '/' + action)) { confirm.value = undefined; emit('changed') }
}
</script>
<template>
  <div class="actions">
    <template v-if="!task.archived">
      <RouterLink v-if="currentRun" class="button primary" :to="'/tasks/' + task.id + '/runs/' + currentRun.id">查看当前执行</RouterLink><button v-else class="primary" :disabled="mutation.busy.value" @click="run">立即运行</button>
      <RouterLink class="button" :to="'/tasks/' + task.id + '/edit'">编辑配置</RouterLink>
      <template v-if="!compact"><button :disabled="mutation.busy.value" @click="toggleSchedule">{{ task.schedule?.enabled ? '关闭定时' : '启用定时' }}</button><button :disabled="!!currentRun || mutation.busy.value" @click="confirm = 'archive'">归档任务</button></template>
    </template>
    <button v-else :disabled="mutation.busy.value" @click="confirm = 'restore'">恢复任务</button>
  </div>
  <p v-if="currentRun && !compact" class="small muted">存在排队、运行或收尾引用，暂不能归档；编辑配置只影响新执行。</p>
  <MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <ModalDialog v-if="confirm" :title="confirm === 'archive' ? '归档任务' : '恢复任务（不是恢复运行记录）'" @close="confirm = undefined"><p>{{ task.name || task.id }} · <code>{{ task.id }}</code></p><p v-if="confirm === 'archive'">归档不删除历史，归档后不再接受新触发。活动引用将由服务端再次检查。</p><p v-else>恢复配置与历史，定时保持关闭；不运行、不入队、不发送 DNS 或通知。DNS 自动推送配置保留。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="confirm = undefined">取消</button><button class="primary" :disabled="mutation.busy.value" @click="archiveRestore">{{ mutation.busy.value ? '提交中…' : '确认' }}</button></div></ModalDialog>
</template>
