<script setup lang="ts">
import { computed, onUnmounted, ref, shallowRef } from 'vue'
import { ApiError, getPendingIds, getUnscopedIds, lookupOperation, watchOperations, getOperationScope, getRecoveryPermits, prepareOperationRecovery, type OperationScope } from '../api/client'
import type { Receipt } from '../api/types'
import ModalDialog from './ModalDialog.vue'
import StatusBadge from './StatusBadge.vue'
const ids = ref(getPendingIds())
const unscopedIds = ref(getUnscopedIds())
const open = ref(false)
const busy = ref(false)
const error = ref('')
const result = shallowRef<Receipt>()
const manualId = ref('')
const selected = ref('')
const recoveryScope = ref<OperationScope>()
const acknowledged = ref(false)
const recoverable = ref(false)
const permits = ref(getRecoveryPermits())
const manualAction = ref('POST /api/v1/connections')
const objectID = ref('')
const actionChoices = [
  ['POST /api/v1/connections','新建连接'],['PATCH /api/v1/connections/{id}','修改连接'],['DELETE /api/v1/connections/{id}','删除连接'],
  ['POST /api/v1/tasks','新建任务'],['PATCH /api/v1/tasks/{id}','修改任务'],['POST /api/v1/tasks/{id}/runs','触发任务'],
  ['POST /api/v1/tasks/{id}/archive','归档任务'],['POST /api/v1/tasks/{id}/restore','恢复任务'],['POST /api/v1/tasks/{id}/schedule-state','切换计划'],
  ['POST /api/v1/runs/{id}/cancel','停止运行'],['POST /api/v1/runs/{id}/restore','恢复记录'],['DELETE /api/v1/runs/{id}','删除记录'],
  ['POST /api/v1/runs/{id}/dns-deliveries','手动DNS投递'],['POST /api/v1/runs/{id}/notifications','手动通知'],
  ['PATCH /api/v1/settings','系统设置'],['POST /api/v1/dns-targets/{id}/auto-write-protection/clear','解除目标保护'],
]
const chosenScope = computed(() => {
  if (recoveryScope.value) return recoveryScope.value
  if (manualAction.value.includes('{id}') && !/^[A-Za-z0-9_-]{1,128}$/.test(objectID.value)) return undefined
  const [method,path] = manualAction.value.replace('{id}',objectID.value).split(' ')
  return method && path ? {method,path} : undefined
})
const unsubscribe = watchOperations(() => { ids.value = getPendingIds(); unscopedIds.value = getUnscopedIds(); permits.value=getRecoveryPermits() })
onUnmounted(unsubscribe)
async function lookup(id: string) {
  busy.value = true; error.value = ''; result.value = undefined; selected.value=id;recoveryScope.value=getOperationScope(id);acknowledged.value=false;recoverable.value=false
  try { result.value = await lookupOperation(id);recoverable.value=!result.value.recovery && ['unknown','expired','unconfirmed'].includes(result.value.state || '') } catch (caught) { error.value = caught instanceof Error ? caught.message : '查询失败';recoverable.value=caught instanceof ApiError && [404,410].includes(caught.status) && ['operation_unknown','operation_expired'].includes(caught.details.code || '') }
  finally { recoveryScope.value=getOperationScope(id);busy.value = false }
}
async function recover() {
  if (!acknowledged.value || !selected.value || !chosenScope.value) return
  busy.value=true;error.value=''
  try {
    result.value=await prepareOperationRecovery(selected.value,chosenScope.value,true)
    recoverable.value=false;acknowledged.value=false
  } catch(caught) {error.value=caught instanceof ApiError ? caught.message+(caught.details.operation_id ? '；核查号：'+caught.details.operation_id : '') : '核查未能确认，请保留原号'}
  finally {busy.value=false}
}
</script>
<template>
  <div class="operation-tracker"><button type="button" :class="{ 'warning-text': ids.length }" @click="open = true">操作核查<template v-if="ids.length">（{{ ids.length }}）</template></button><span v-if="ids.length" class="small warning-text" role="status">请按原号核查，不要重复提交。</span></div>
  <ModalDialog v-if="open" title="原操作号只读核查" @close="open = false">
    <p>本机仅保存操作号和不可逆动作标识，不保存密码、连接凭据或完整提交参数。凭据动作只按非敏感操作范围关联，不摘要凭据；刷新或重新登录后也不会自动重交。</p>
    <p v-if="unscopedIds.length" class="notice warning">旧版记录缺少动作范围。完成原号核查前暂不允许新建任务或连接，其他对象操作仍可继续。</p>
    <p class="notice warning">unknown／expired／未找到均不能证明从未执行，不会自动换号重做。先核查原对象；仅在明确确认风险后，许可一次保留原关联的新尝试。</p>
    <ul class="plain-list"><li v-for="id in ids" :key="id" class="list-row"><code>{{ id }}</code><button type="button" :disabled="busy" @click="lookup(id)">只读查询</button></li></ul>
    <p v-if="!ids.length" class="muted">当前没有待核查操作号。</p>
    <form class="stack" @submit.prevent="lookup(manualId.trim())"><label for="operation-id">按已知操作号查询</label><input id="operation-id" v-model="manualId" required autocomplete="off" /><button :disabled="busy || !manualId.trim()">查询收据</button></form>
    <p v-if="error" class="notice danger" role="alert">{{ error }}。保留原号，不代表未执行。</p>
    <section v-if="recoverable && ids.includes(selected)" class="notice warning">
      <h3>人工核查后准备新尝试</h3><p>原号：<code>{{ selected }}</code>。先前往对应任务、连接或投递详情核对实际对象；本操作不会运行任务、修改 DNS 或发消息。</p>
      <p v-if="recoveryScope">范围：{{ recoveryScope.method }} <code>{{ recoveryScope.path }}</code></p>
      <template v-else><label>旧记录缺少范围，请明确选择<select v-model="manualAction" @change="acknowledged=false"><option v-for="choice in actionChoices" :key="choice[0]" :value="choice[0]">{{ choice[1] }}</option></select></label><label v-if="manualAction.includes('{id}')">原任务／运行／连接／目标 ID<input v-model.trim="objectID" autocomplete="off" maxlength="128" @input="acknowledged=false" /></label><p>范围 {{ chosenScope ? chosenScope.method + ' ' + chosenScope.path : '尚未填写完整对象标识' }}；服务端仍检查原收据与实际业务权限，不因核查绕过投递或取消限制。</p></template>
      <p>原动作可能已完成或仍在远端执行。新尝试可能创建重复对象或覆盖结果；旧号不会被当作未执行，也不会重新受理。</p>
      <label class="check-line"><input v-model="acknowledged" type="checkbox" />我已人工核查原对象，理解可能重复，允许一次同范围新尝试并保留原号关联</label>
      <button class="danger-button" :disabled="busy || !acknowledged || !chosenScope" @click="recover">确认核查并准备一次新尝试</button>
    </section>
    <section v-if="permits.length" class="notice"><h3>已确认、尚未提交的新尝试</h3><p>请返回对应表单重新核对并手动提交；页面不会代你执行。</p><ul><li v-for="permit in permits" :key="permit.id">{{ permit.method }} {{ permit.path }} · 原号 {{ permit.prior_operation_id }} · 核查号 {{ permit.id }}</li></ul></section>
    <div v-if="result" class="notice"><StatusBadge :value="result.state" /><p>原号：<code>{{ result.operation_id }}</code></p><p>关联对象：<code>{{ result.object_id || '未提供' }}</code></p><pre v-if="result.result">{{ JSON.stringify(result.result, null, 2) }}</pre></div>
  </ModalDialog>
</template>
