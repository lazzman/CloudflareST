<script setup lang="ts">
import type { Target } from '../api/types'
import { targetName } from '../lib/domain'
import { date } from '../lib/settings'
defineProps<{ target: Target }>()
</script>
<template>
  <dl class="facts">
    <div><dt>现有记录</dt><dd>{{ targetName(target) }}</dd></div>
    <div><dt>连接 / 账号</dt><dd><code>{{ target.connection_id }}</code> / {{ target.account_id || '未知账号' }}</dd></div>
    <div><dt>最近一次读取 IP</dt><dd><code>{{ target.value || '未知／不可读取' }}</code>（不代表此刻远端值）</dd></div>
    <div><dt>服务端读取时刻</dt><dd>{{ date(target.read_at) }}</dd></div>
    <div><dt>TTL / 线路</dt><dd>{{ target.ttl ?? '未知' }} / {{ target.line || '未知' }}（不修改）</dd></div>
    <div><dt>last_known_write</dt><dd>任务 <code>{{ target.last_known_write?.task_id || '未知' }}</code> · 执行 <code>{{ target.last_known_write?.run_id || '未知' }}</code><br />已确认写入：{{ date(target.last_known_write?.written_at) }} · <code>{{ target.last_known_write?.ip || '未知 IP' }}</code></dd></div>
  </dl>
  <p class="small muted">最近写入仅代表本系统已确认的事实，不涵盖外部改动；读到相同 IP 不推断写入人。</p>
  <div v-if="target.protection?.active" class="notice warning"><strong>自动写入暂停，待人工处理</strong><p>{{ target.protection.reason || '原结果未确认' }}</p><p>原尝试：<code>{{ target.protection.attempt_id || '未知' }}</code> · 形成于 {{ date(target.protection.created_at) }} · 版本 {{ target.protection.revision ?? '未知' }}</p><RouterLink v-if="target.protection.attempt_id" :to="{ path: '/operation-logs', query: { object_id: target.protection.attempt_id } }">查看原未确认操作日志</RouterLink></div>
  <p v-else class="small">自动写入保护：{{ target.protection?.active === false ? '当前未暂停' : '状态未知，服务端提交时核验' }}</p>
</template>
