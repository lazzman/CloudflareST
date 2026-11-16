<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { request, query, segment } from '../api/client'
import type { Candidate, Page } from '../api/types'
import { metric } from '../lib/domain'
import { useResource } from '../lib/useResource'
import ResourceState from './ResourceState.vue'
import PagerControl from './PagerControl.vue'
const props = defineProps<{ taskId: string; runId: string; recordType?: string; modelValue?: string }>()
const emit = defineEmits<{ 'update:modelValue': [string] }>()
const page = ref(1)
const family = computed(() => props.recordType === 'AAAA' ? 'ipv6' : 'ipv4')
const resource = useResource(() => request<Page<Candidate>>('/tasks/' + segment(props.taskId) + '/runs/' + segment(props.runId) + '/candidates' + query({page: page.value, page_size: 50, family: family.value, eligibility: 'eligible'})), {key: () => family.value + ':' + page.value})
watch(() => resource.data.value?.page, value => { if (value && value !== page.value) page.value = value })
const choices = computed(() => resource.data.value?.items || [])
</script>
<template>
  <ResourceState :loading="resource.loading.value" :error="resource.error.value" @retry="resource.reload" />
  <label>匹配此 {{ recordType }} 的最终合格 IP<select :value="modelValue || ''" :disabled="resource.loading.value || !!resource.error.value" @change="emit('update:modelValue', ($event.target as HTMLSelectElement).value)"><option value="">明确选择一个合格地址</option><option v-if="modelValue && !choices.some(item => item.ip === modelValue)" :value="modelValue">{{ modelValue }}（已选，位于其他页）</option><option v-for="candidate in choices" :key="candidate.ip" :value="candidate.ip">{{ candidate.ip }} · {{ metric(candidate.latency_ms, ' ms') }} / {{ metric(candidate.speed_mb_s, ' MB/s') }}</option></select></label>
  <PagerControl v-if="resource.data.value && resource.data.value.total > 50" :page="page" :total="resource.data.value.total" :size="50" @change="page = $event" />
</template>
