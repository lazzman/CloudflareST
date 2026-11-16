<script setup lang="ts">
import type { Candidate, OptimizerConfig, SourceResult } from '../api/types'
import { label, metric } from '../lib/domain'
defineProps<{ candidate: Candidate; config?: Partial<OptimizerConfig>; sources?: SourceResult[] }>()
function enabled(config: Partial<OptimizerConfig> | undefined, stage?: string) {
  const value = stage === 'latency' ? config?.latency_enabled : stage === 'loss' ? config?.loss_enabled : stage === 'download' ? config?.download_enabled : undefined
  return value === undefined ? '基础／采样阶段' : value ? '已启用' : '已停用（不视为通过）'
}
function threshold(config: Partial<OptimizerConfig> | undefined, stage?: string) {
  return stage === 'latency' ? '≤ ' + metric(config?.latency_max_ms, ' ms') : stage === 'loss' ? '≤ ' + metric(config?.loss_max_percent, '%') : stage === 'download' ? '≥ ' + metric(config?.speed_min_mb_s, ' MB/s') : '不适用'
}
function actual(candidate: Candidate, stage?: string) {
  return stage === 'latency' ? metric(candidate.latency_ms, ' ms') : stage === 'loss' ? metric(candidate.loss_percent, '%') : stage === 'download' ? metric(candidate.speed_mb_s, ' MB/s') : '见测量／来源事实'
}
</script>
<template>
  <details><summary>全部命中来源（{{ candidate.sources?.length ?? '未知' }}）</summary><ul><li v-for="sourceId in candidate.sources || []" :key="sourceId"><code>{{ sourceId }}</code><template v-for="source in sources?.filter(item => item.id === sourceId) || []" :key="source.id"> · {{ source.source?.kind }} · {{ source.source?.value }}</template></li></ul><p v-if="!candidate.sources?.length">本记录未提供来源标记，不推断来源。</p><p>生成方式：{{ candidate.origin === 'expanded' ? '单轮邻近扩采，触发信息见预算明细' : candidate.origin || '未知' }}</p></details>
  <details v-for="(step, index) in candidate.steps || []" :key="index"><summary>{{ index + 1 }}. {{ label(step.stage) }}：{{ label(step.state) }}</summary><p>{{ enabled(config, step.stage) }}；当次阈值 {{ threshold(config, step.stage) }}；实际 {{ actual(candidate, step.stage) }}</p><p>{{ step.reason || '无额外原因' }}</p></details>
  <p v-if="candidate.reason">最终原因：{{ candidate.reason }}</p>
  <p v-if="!candidate.steps?.length && !candidate.reason">服务端未提供步骤详情。</p>
</template>
