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
import PendingOperationNotice from '../components/PendingOperationNotice.vue'
import StatusBadge from '../components/StatusBadge.vue'
import { elapsedSince } from '../lib/runFacts'
const resource = useResource(() => request<Queue>('/queue'), { poll: () => true })
const mutation = useMutation()
const confirm = ref<Run>()
const current = computed(() => resource.data.value?.current)
const waiting = computed(() => resource.data.value?.waiting || [])
const nextWaiting = computed(() => waiting.value[0])
const serverNow = computed(() => resource.data.value?.server_time ? Date.parse(resource.data.value.server_time) : Date.now())
async function cancel() {
  if (!confirm.value) return
  const run = confirm.value
  if (await mutation.submit('/runs/' + segment(run.id) + '/cancel')) { confirm.value = undefined; void resource.reload() }
}
</script>
<template>
  <header class="page-heading"><div><h1 tabindex="-1">运行队列</h1><p class="muted">全局串行执行。当前执行释放席位后，等待项按入队顺序自动开始；自动收尾期间仍占用席位。</p></div></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" /><MutationState :error="mutation.error.value" :receipt="mutation.receipt.value" />
  <PendingOperationNotice scope="/api/v1/runs" label="运行队列" @resolved="resource.reload" />

  <section class="section queue-section">
    <header class="queue-head"><div><h2>当前执行 <StatusBadge v-if="current" :value="current.status" /></h2><p class="small muted">当前占用的执行；收尾阶段也在此显示。</p></div></header>
    <article v-if="current" class="queue-current">
      <div class="queue-current-main">
        <div><RouterLink :to="'/tasks/' + current.task_id" class="task-name">{{ current.task_name || current.task_id }}</RouterLink><p class="small muted queue-run-meta"><RouterLink :to="'/tasks/' + current.task_id + '/runs/' + current.id"><code>{{ current.id }}</code></RouterLink> · 触发 {{ label(current.trigger) }}</p></div>
        <div class="queue-runtime"><strong>{{ elapsedSince(current.started_at, serverNow) }}</strong><span class="small muted">已运行</span></div>
      </div>
      <dl class="facts queue-facts">
        <div><dt>阶段</dt><dd>{{ label(current.stage) }} · {{ current.progress?.family?.toUpperCase() || '未知地址族' }}</dd></div>
        <div><dt>进度</dt><dd>{{ current.progress?.done ?? '未知' }} / {{ current.progress?.total ?? '待确定' }} {{ current.progress?.unit || '' }}</dd></div>
        <div><dt>开始</dt><dd>{{ date(current.started_at) }}</dd></div>
        <div><dt>席位</dt><dd>{{ current.slot_busy ? '占用中（含自动收尾）' : '未占用' }}</dd></div>
      </dl>
      <p class="queue-message">{{ current.progress?.message || '未提供阶段说明' }}</p>
      <StageProgressTable :stages="current.stage_progress" />
      <p class="small muted queue-note">不显示总体百分比或预计剩余时间；失败、跳过和未测试不冒充合格或 0。</p>
    </article>
    <div v-else-if="resource.data.value" class="queue-idle">
      <div><p class="queue-idle-title">当前席位空闲。</p><p v-if="nextWaiting" class="small muted">下一位：<RouterLink :to="'/tasks/' + nextWaiting.task_id">{{ nextWaiting.task_name || nextWaiting.task_id }}</RouterLink>，轮到时会自动开始。</p><p v-else class="small muted">当前没有等待项。运行策略后，排队中的执行会显示在这里。</p></div>
      <RouterLink v-if="nextWaiting" :to="'/tasks/' + nextWaiting.task_id + '/runs/' + nextWaiting.id" class="button">查看下一项</RouterLink><RouterLink v-else to="/tasks" class="button">去运行策略</RouterLink>
    </div>
  </section>

  <section class="section queue-section">
    <header class="queue-head"><div><h2>等待执行 <span class="panel-meta">{{ waiting.length }} 项</span></h2><p class="small muted">按入队顺序自动开始；取消只影响所选等待项，不停止当前执行。</p></div></header>
    <table v-if="waiting.length" class="responsive-table queue-table">
      <caption class="sr-only">等待执行的运行，按入队顺序排列</caption>
      <thead><tr><th>顺序</th><th>策略 / 执行</th><th>触发</th><th>入队时间</th><th>已等待</th><th>操作</th></tr></thead>
      <tbody><tr v-for="(run, index) in waiting" :key="run.id">
        <td data-label="顺序"><strong>第 {{ index + 1 }} 位</strong><span v-if="index === 0" class="block small queue-next">下一位</span></td>
        <td data-label="策略 / 执行"><RouterLink :to="'/tasks/' + run.task_id" class="task-name">{{ run.task_name || run.task_id }}</RouterLink><span class="block small"><RouterLink :to="'/tasks/' + run.task_id + '/runs/' + run.id"><code>{{ run.id }}</code></RouterLink> · <StatusBadge :value="run.status" /></span></td>
        <td data-label="触发">{{ label(run.trigger) }}</td>
        <td data-label="入队时间">{{ date(run.queued_at) }}</td>
        <td data-label="已等待">{{ elapsedSince(run.queued_at, serverNow) }}</td>
        <td data-label="操作"><div class="queue-actions"><RouterLink :to="'/tasks/' + run.task_id + '/runs/' + run.id">查看执行</RouterLink><button type="button" :disabled="mutation.busy.value" @click="confirm = run">取消此排队执行</button></div></td>
      </tr></tbody>
    </table>
    <div v-else-if="resource.data.value" class="queue-idle"><div><p class="queue-idle-title">没有等待项。</p><p class="small muted">运行策略后，排队中的执行会按入队顺序显示在这里。</p></div><RouterLink to="/tasks" class="button">去运行策略</RouterLink></div>
  </section>

  <ModalDialog v-if="confirm" title="取消排队中的执行" @close="confirm = undefined"><p>任务：{{ confirm.task_name || confirm.task_id }} · 执行：<code>{{ confirm.id }}</code></p><p>仅取消尚未取得席位的本次执行；不会停止当前任务或删除其他运行记录。</p><MutationState :error="mutation.error.value" /><div class="actions"><button class="safe-focus" @click="confirm = undefined">返回</button><button class="danger-button" :disabled="mutation.busy.value" @click="cancel">确认取消本次排队</button></div></ModalDialog>
</template>
