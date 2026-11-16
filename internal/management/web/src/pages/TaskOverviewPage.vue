<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { request, segment } from '../api/client'
import type { Queue, Task } from '../api/types'
import { useResource } from '../lib/useResource'
import { label, scheduleText } from '../lib/domain'
import { date } from '../lib/settings'
import TaskActions from '../components/TaskActions.vue'
import ResourceState from '../components/ResourceState.vue'
import TargetFacts from '../components/TargetFacts.vue'
import ProtectionControl from '../components/ProtectionControl.vue'
const route = useRoute()
const id = String(route.params.taskId)
const resource = useResource(async () => ({ task: await request<Task>('/tasks/' + segment(id)), queue: await request<Queue>('/queue') }), { poll: () => true })
const task = computed(() => resource.data.value?.task)
const currentRun = computed(() => [resource.data.value?.queue.current, ...(resource.data.value?.queue.waiting || [])].find(run => run?.task_id === id))
</script>
<template>
  <RouterLink to="/tasks">← 优选任务</RouterLink>
  <header class="page-heading"><div><p class="eyebrow">任务概览 · {{ task?.archived ? '已归档' : '活跃' }}</p><h1 tabindex="-1">{{ task?.name || '任务概览' }}</h1><code>{{ id }}</code> · 配置版本 {{ task?.revision ?? '未知' }}</div></header>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" />
  <p v-if="task?.configuration_issue" class="notice danger" role="alert">{{ task.configuration_issue }}；敏感地址已移除，定时暂停。请重新填写安全地址并保存；不会自动重跑或恢复计划。</p>
  <template v-if="task">
    <TaskActions :task="task" :current-run="currentRun" @changed="resource.reload" />
    <nav class="subnav" aria-label="任务页面"><RouterLink :to="'/tasks/' + id + '/runs'">运行记录</RouterLink><RouterLink :to="'/tasks/' + id + '/recycle-bin'">该任务回收站</RouterLink></nav>
    <section class="section"><h2>计划与自动动作</h2><p class="small muted">关闭定时只停止未来触发，不取消已有执行，不关闭 DNS 或通知。</p><dl class="facts"><div><dt>定时计划</dt><dd>{{ task.schedule?.enabled ? '开启' : '关闭' }} · {{ scheduleText(task) }}</dd></div><div><dt>下次未来时点</dt><dd>{{ task.schedule?.enabled ? date(task.schedule.next_run_at) : '未启用' }}</dd></div><div><dt>DNS 自动推送</dt><dd>{{ task.dns_auto ? '开启' : '关闭' }}</dd></div><div><dt>自动通知</dt><dd>Telegram {{ task.notifications?.telegram?.enabled ? '开启' : '关闭' }} / 企业微信 {{ task.notifications?.wecom?.enabled ? '开启' : '关闭' }}</dd></div><div><dt>当前执行</dt><dd v-if="currentRun"><RouterLink :to="'/tasks/' + id + '/runs/' + currentRun.id">{{ label(currentRun.status) }} · {{ label(currentRun.stage) }}</RouterLink> · 席位 {{ currentRun.slot_busy ? '占用' : '未占用' }}</dd><dd v-else>当前无活动执行</dd></div></dl></section>
    <section class="section"><h2>来源与筛选规则</h2><dl class="facts"><div><dt>地址族</dt><dd>{{ task.config?.ipv4_enabled ? 'IPv4 ' : '' }}{{ task.config?.ipv6_enabled ? 'IPv6' : '' }}</dd></div><div><dt>来源</dt><dd><ul><li v-for="(source, index) in task.config?.sources" :key="index">{{ source.kind }} · <code>{{ source.value }}</code></li></ul></dd></div><div><dt>过滤顺序</dt><dd>{{ task.config?.filter_order?.map(label).join(' → ') || '未知' }}</dd></div><div><dt>门槛</dt><dd>延迟 {{ task.config?.latency_enabled ? '≤ ' + task.config.latency_max_ms + ' ms' : '关闭' }} / 失败比例 {{ task.config?.loss_enabled ? '≤ ' + task.config.loss_max_percent + '%' : '关闭' }} / 下载 {{ task.config?.download_enabled ? '≥ ' + task.config.speed_min_mb_s + ' MB/s' : '关闭（未测）' }}</dd></div><div><dt>测速文件 URL</dt><dd><code>{{ task.config?.download_url || '未知' }}</code></dd></div><div><dt>排序</dt><dd>{{ task.config?.sort === 'speed' ? '速度优先' : task.config?.sort === 'latency' ? '延迟优先' : '未知' }}</dd></div></dl></section>
    <section class="section"><h2>已配置 DNS 目标</h2><p v-if="!task.targets?.length" class="muted">未配置目标。手动推送在指定运行详情中明确选择现有记录。</p><article v-for="target in task.targets" :key="target.target_id || target.record_id" class="target-entry"><TargetFacts :target="target" /><ProtectionControl :target="target" @changed="resource.reload" /></article></section>
  </template>
</template>
