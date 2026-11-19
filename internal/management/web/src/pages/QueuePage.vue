<script setup lang="ts">
import { computed, ref } from 'vue'
import { request, segment } from '../api/client'
import type { Queue, Run } from '../api/types'
import { useResource } from '../lib/useResource'
import { label } from '../lib/domain'
import { date } from '../lib/settings'
import { useMutation } from '../lib/useMutation'
import ResourceState from '../components/ResourceState.vue'
import MutationState from '../components/MutationState.vue'
import ModalDialog from '../components/ModalDialog.vue'
import StageProgressTable from '../components/StageProgressTable.vue'
import { elapsedSince } from '../lib/runFacts'
const resource = useResource(() => request<Queue>('/queue'), { poll: () => true })
const mutation = useMutation()
const confirm = ref<Run>()
const waiting = computed(() => resource.data.value?.waiting || [])
const serverNow = computed(() => resource.data.value?.server_time ? Date.parse(resource.data.value.server_time) : Date.now())
async function cancel() {
  if (!confirm.value) return
  const run = confirm.value
  if (await mutation.submit('/runs/' + segment(run.id) + '/cancel')) { confirm.value = undefined; void resource.reload() }
}
</script>
<template><header class="page-heading"><div><h1 tabindex="-1">运行队列</h1><p class="muted">全局串行，不跨任务查询历史。运行已产生合格 IP 时仍可能正在自动收尾；未释放席位之前不可启动同一任务的下一次执行。</p></div></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <section class="section"><h2>当前席位</h2><article v-if="resource.data.value?.current" class="run-row"><div><RouterLink :to="'/tasks/' + resource.data.value.current.task_id"><strong>{{ resource.data.value.current.task_name || resource.data.value.current.task_id }}</strong></RouterLink> · <RouterLink :to="'/tasks/' + resource.data.value.current.task_id + '/runs/' + resource.data.value.current.id"><code>{{ resource.data.value.current.id }}</code></RouterLink></div><p>主状态：{{ label(resource.data.value.current.status) }} · 阶段：{{ label(resource.data.value.current.stage) }}</p><p>阶段进度：{{ resource.data.value.current.progress?.family || '未知地址族' }} {{ resource.data.value.current.progress?.done ?? '未知' }} / {{ resource.data.value.current.progress?.total ?? '待确定总量' }} {{ resource.data.value.current.progress?.unit || '' }}</p><p>{{ resource.data.value.current.progress?.message || '未提供阶段说明' }}</p><p>运行已用时：{{ elapsedSince(resource.data.value.current.started_at, serverNow) }}（含自动收尾）</p><StageProgressTable :stages="resource.data.value.current.stage_progress" /><p>开始：{{ date(resource.data.value.current.started_at) }} · 当前席位：{{ resource.data.value.current.slot_busy ? '仍占用（含自动收尾）' : '未占用' }}</p><p class="notice warning">不显示总体百分比或预计剩余时间；失败、跳过和未测试不冒充合格或 0。</p></article><p v-else-if="resource.data.value" class="empty-state">当前席位空闲。</p></section>
  <section class="section"><h2>FIFO 等待队列（{{ waiting.length }}）</h2><ol v-if="waiting.length" class="plain-list"><li v-for="(run, index) in waiting" :key="run.id" class="run-row"><div><strong>第 {{ index + 1 }} 位</strong> · <RouterLink :to="'/tasks/' + run.task_id"><strong>{{ run.task_name || run.task_id }}</strong></RouterLink> · <code>{{ run.id }}</code></div><p>已等待：{{ elapsedSince(run.queued_at, serverNow) }}</p><p>入队：{{ date(run.queued_at) }} · 触发：{{ label(run.trigger) }} · 状态：{{ label(run.status) }}</p><RouterLink :to="'/tasks/' + run.task_id + '/runs/' + run.id">查看执行</RouterLink> <button type="button" :disabled="mutation.busy.value" @click="confirm = run">取消此排队执行</button></li></ol><p v-else-if="resource.data.value" class="empty-state">没有等待项。取消排队不占用席位，也不触碰其他运行。</p></section>
  <ModalDialog v-if="confirm" title="取消排队中的执行" @close="confirm = undefined"><p>任务：{{ confirm.task_name || confirm.task_id }} · 执行：<code>{{ confirm.id }}</code></p><p>仅取消尚未取得席位的本次执行；不会停止当前任务或删除其他运行记录。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="confirm = undefined">返回</button><button class="danger-button" :disabled="mutation.busy.value" @click="cancel">确认取消本次排队</button></div></ModalDialog>
</template>
