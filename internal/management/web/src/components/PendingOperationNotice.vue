<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { ApiError, getPendingOperations, lookupOperation, watchOperations, type PendingOperation } from '../api/client'
const props = defineProps<{ scope: string; exact?: boolean; label?: string }>()
const emit = defineEmits<{ resolved: [] }>()
const items = ref<PendingOperation[]>([])
const busy = ref(false)
const message = ref('')
const attempted = new Set<string>()
let hadItems = false
let stopped = false
let unsubscribe = () => {}
function sync() {
  const next = getPendingOperations(props.scope, props.exact)
  const resolved = hadItems && !next.length
  items.value = next
  hadItems = next.length > 0
  if (resolved && !stopped) emit('resolved')
}
async function query(id: string) {
  try {
    const receipt = await lookupOperation(id)
    if (['unknown', 'expired', 'unconfirmed', 'pending', 'retired'].includes((receipt.state || '').toLowerCase())) message.value = '服务端仍未给出确定结果，请核对本页数据。'
  } catch (error) {
    message.value = error instanceof ApiError ? error.message : '查询中断，请稍后重试。'
  }
}
async function run() {
  const fresh = items.value.filter(item => !attempted.has(item.id))
  fresh.forEach(item => attempted.add(item.id))
  if (!fresh.length) return
  busy.value = true
  message.value = ''
  try { await Promise.all(fresh.map(item => query(item.id))) }
  finally { busy.value = false; sync() }
}
function refresh() { attempted.clear(); void run() }
onMounted(() => {
  sync()
  unsubscribe = watchOperations(sync)
  void run()
})
onUnmounted(() => { stopped = true; unsubscribe() })
</script>
<template>
  <section v-if="items.length" class="notice warning pending-operation" role="region" aria-label="未确认写入">
    <p><strong>{{ label || '本页' }}有 {{ items.length }} 次写入结果未确认。</strong>系统已自动查询上次结果；请先核对本页数据，不要重复提交。</p>
    <p v-if="message" class="small">{{ message }}</p>
    <details class="small"><summary>查看操作号</summary><ul class="plain-list"><li v-for="item in items" :key="item.id"><code>{{ item.id }}</code></li></ul></details>
    <button type="button" :disabled="busy" @click="refresh">{{ busy ? '查询中…' : '重新查询' }}</button>
  </section>
</template>
