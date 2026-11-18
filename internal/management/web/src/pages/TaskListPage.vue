<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { request } from '../api/client'
import type { Queue, Task } from '../api/types'
import { useResource } from '../lib/useResource'
import { scheduleText } from '../lib/domain'
import { automaticActions } from '../lib/resultView'
import { date } from '../lib/settings'
import StrategyActions from '../components/StrategyActions.vue'
import ResourceState from '../components/ResourceState.vue'
import StatusBadge from '../components/StatusBadge.vue'
import AppIcon from '../components/AppIcon.vue'
import PageHeader from '../components/PageHeader.vue'
import SurfacePanel from '../components/SurfacePanel.vue'
const route = useRoute(), router = useRouter()
const search = ref('')
const archived = computed(() => route.query.view === 'archived')
const resource = useResource(async () => { const [tasks, queue] = await Promise.all([request<Task[]>('/tasks?view=strategies'), request<Queue>('/queue')]); return {tasks, queue} }, {poll: () => true})
const shown = computed(() => resource.data.value?.tasks.filter(task => !!task.archived === archived.value && ((task.name || '').toLowerCase().includes(search.value.toLowerCase()) || task.id.toLowerCase().includes(search.value.toLowerCase()))) || [])
const current = (id: string) => [resource.data.value?.queue.current, ...(resource.data.value?.queue.waiting || [])].find(run => run?.task_id === id) || undefined
</script>
<template>
  <PageHeader title="优选策略" desc="设置定时，直接查看每次优选的 IP 列表。"><RouterLink v-if="!archived" to="/tasks/new" class="button primary"><AppIcon name="plus" />新建策略</RouterLink></PageHeader>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" :updated-at="resource.updatedAt.value" @retry="resource.reload" />
  <SurfacePanel :title="archived ? '已归档策略' : '我的策略'" :meta="resource.data.value ? shown.length + ' 个' : '等待读取'" :aria-busy="resource.loading.value || resource.refreshing.value">
    <template #tools><button class="quiet-button" :disabled="resource.loading.value" @click="resource.reload"><AppIcon name="refresh" :size="16" />刷新</button></template>
    <div class="toolbar task-toolbar"><label class="search-control"><span class="sr-only">查找名称／ID</span><AppIcon name="search" :size="17" /><input v-model="search" type="search" placeholder="搜索策略名称或 ID" /></label><button class="quiet-button" @click="router.replace({query: archived ? {} : {view: 'archived'}})">{{ archived ? '返回我的策略' : '已归档' }}</button></div>
    <table v-if="resource.data.value && shown.length" class="responsive-table strategy-table"><caption class="sr-only">策略计划与最近运行</caption><thead><tr><th>策略</th><th>定时计划</th><th>最近运行</th><th>操作</th></tr></thead><tbody><tr v-for="task in shown" :key="task.id"><td data-label="策略"><RouterLink class="task-name" :to="'/tasks/' + task.id + '/results'">{{ task.name || '未命名策略' }}</RouterLink><div class="family-tags"><span v-if="task.config?.ipv4_enabled">IPv4</span><span v-if="task.config?.ipv6_enabled">IPv6</span></div><p v-if="task.configuration_issue" class="field-error">{{ task.configuration_issue }}</p><p v-if="automaticActions(task)" class="small automatic-summary">运行后自动推送：{{ automaticActions(task) }}</p></td><td data-label="定时计划">{{ scheduleText(task) }}<span class="block small">{{ task.schedule?.enabled ? '下次 ' + date(task.schedule.next_run_at) : '定时已关闭' }}</span></td><td data-label="最近运行"><template v-if="task.latest_run"><StatusBadge :value="task.latest_run.status" /><span class="block small">{{ date(task.latest_run.queued_at) }}</span></template><span v-else class="muted">尚未运行</span></td><td data-label="操作"><StrategyActions :task="task" :current-run="current(task.id)" @changed="resource.reload" /></td></tr></tbody></table>
    <section v-else-if="resource.data.value && !resource.error.value" class="empty-state"><h2>{{ search ? '没有匹配策略' : archived ? '没有归档策略' : '还没有优选策略' }}</h2><p>{{ search ? '换个名称或 ID 试试。' : '新建策略，保存后开启定时，或先运行一次。' }}</p></section>
  </SurfacePanel>
</template>
