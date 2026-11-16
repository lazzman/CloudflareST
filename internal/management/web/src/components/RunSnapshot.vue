<script setup lang="ts">
import type { Task } from '../api/types'
import { label, targetName } from '../lib/domain'
defineProps<{ task?: Task }>()
const state = (value?: boolean) => value === undefined ? '未知' : value ? '启用' : '停用'
</script>
<template>
  <details class="snapshot-details"><summary>完整当次配置快照（不随任务当前配置变化）</summary>
    <dl class="facts">
      <div><dt>地址族</dt><dd>IPv4 {{ state(task?.config?.ipv4_enabled) }} / IPv6 {{ state(task?.config?.ipv6_enabled) }}</dd></div>
      <div><dt>抽样与预算</dt><dd>每段默认 {{ task?.config?.sample_size ?? '未知' }}；IPv4 {{ task?.config?.ipv4_limit ?? '未知' }} / IPv6 {{ task?.config?.ipv6_limit ?? '未知' }}</dd></div>
      <div><dt>延迟测量</dt><dd>{{ task?.config?.probe_mode === 'tcp' ? 'TCPING' : task?.config?.probe_mode === 'icmp' ? 'ICMP Ping' : '未知' }} · 端口 {{ task?.config?.tcp_port ?? '未知' }} · 次数 {{ task?.config?.probe_count ?? '未知' }} · 每次等待 {{ task?.config?.probe_timeout_ms ?? '未知' }} ms</dd></div>
      <div><dt>过滤顺序与启停</dt><dd>{{ task?.config?.filter_order?.map(label).join(' → ') || '未知' }}<br />延迟 {{ state(task?.config?.latency_enabled) }} / 失败比例 {{ state(task?.config?.loss_enabled) }} / 下载 {{ state(task?.config?.download_enabled) }}</dd></div>
      <div><dt>下载配额与窗口</dt><dd>IPv4 {{ task?.config?.download_ipv4_count ?? '未知' }} / IPv6 {{ task?.config?.download_ipv6_count ?? '未知' }}；正文 {{ task?.config?.download_window_seconds ?? '未知' }} s；正文前 {{ task?.config?.prebody_timeout_seconds ?? '未知' }} s</dd></div>
      <div><dt>当次排序</dt><dd>{{ task?.config?.sort === 'speed' ? '速度优先，延迟次序' : task?.config?.sort === 'latency' ? '延迟优先，速度次序' : '未知' }}；再按 IP 数值；关闭下载时不使用速度</dd></div>
      <div><dt>当次自动推送开关</dt><dd>DNS {{ state(task?.dns_auto) }} / Telegram {{ state(task?.notifications?.telegram?.enabled) }} / 企业微信 {{ state(task?.notifications?.wecom?.enabled) }}</dd></div>
    </dl>
    <h4>每条来源及初采覆盖值</h4><ul><li v-for="(source, index) in task?.config?.sources" :key="index">{{ source.kind }} · {{ source.value }} · {{ source.count ? '独立初采 ' + source.count : '使用当次默认初采' }}</li></ul>
    <h4>当次非敏感投递引用</h4><ul><li v-for="target in task?.targets || []" :key="target.target_id || target.record_id">{{ targetName(target) }} · 连接 {{ target.connection_id }}（入队时选择，不代表现在的远端值）</li></ul>
    <p>Telegram 连接：{{ task?.notifications?.telegram?.connection_id || '未配置' }}；企业微信连接：{{ task?.notifications?.wecom?.connection_id || '未配置' }}</p>
    <details><summary>完整非敏感字段</summary><pre>{{ JSON.stringify(task, null, 2) }}</pre></details>
  </details>
</template>
