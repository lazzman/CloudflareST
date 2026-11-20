<script setup lang="ts">
import type { StageProgress } from '../api/types'
import { label } from '../lib/domain'
defineProps<{ stages?: StageProgress[] }>()
</script>
<template>
  <table v-if="stages?.length" class="responsive-table">
    <caption>各地址族与阶段的真实计数（成功处理不等于最终合格）</caption>
    <thead><tr><th>阶段 / 地址族</th><th>已处理 / 已知总量</th><th>实际结果计数</th></tr></thead>
    <tbody><tr v-for="item in stages" :key="item.stage + ':' + item.family + ':' + item.unit">
      <td data-label="阶段 / 族">{{ label(item.stage) }} · {{ item.family?.toUpperCase() || '公共阶段' }}</td>
      <td data-label="进度">{{ item.done ?? '未知' }} / {{ item.total ?? '待确定' }} {{ item.unit }}<span class="block small">{{ item.message }}</span></td>
      <td data-label="结果计数"><template v-if="item.counts">成功处理 {{ item.counts.succeeded }} · 失败 {{ item.counts.failed }} · 跳过 {{ item.counts.skipped }} · 淘汰 {{ item.counts.filtered }} · 未确认 {{ item.counts.unconfirmed }}</template><span v-else>该阶段未提供分类计数</span><span class="block small">最终合格：{{ item.qualified ?? '尚未定稿／不适用' }}</span></td>
    </tr></tbody>
  </table>
  <p v-else class="muted">尚未产生阶段明细；历史记录缺失的计数不补为 0。</p>
</template>
