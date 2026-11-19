<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref } from 'vue'
import { lockBackgroundScroll } from '../lib/scroll'
defineProps<{ title: string; size?: 'narrow' }>()
const emit = defineEmits<{ close: [] }>()
const dialog = ref<HTMLDialogElement>()
const previous = document.activeElement as HTMLElement | null
let unlockScroll = () => {}
function trapFocus(event: KeyboardEvent) {
  if (event.key !== 'Tab') return
  const elements = dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])')
  if (!elements?.length) return
  const focusable = [...elements]
  const first = focusable[0]!
  const last = focusable[focusable.length - 1]!
  if (event.shiftKey && (document.activeElement === first || !dialog.value?.contains(document.activeElement))) {
    event.preventDefault()
    last.focus()
  } else if (!event.shiftKey && (document.activeElement === last || !dialog.value?.contains(document.activeElement))) {
    event.preventDefault()
    first.focus()
  }
}
onMounted(async () => {
  unlockScroll = lockBackgroundScroll()
  dialog.value?.showModal()
  await nextTick()
  const focus = dialog.value?.querySelector<HTMLElement>('.safe-focus') || dialog.value?.querySelector<HTMLElement>('h2')
  focus?.focus()
})
onUnmounted(() => {
  dialog.value?.close()
  unlockScroll()
  if (previous?.isConnected) previous.focus({ preventScroll: true }); else document.querySelector<HTMLElement>('h1')?.focus({ preventScroll: true })
})
</script>
<template>
  <dialog ref="dialog" :class="size ? 'dialog-' + size : undefined" aria-labelledby="dialog-title" @keydown="trapFocus" @cancel.prevent="emit('close')">
    <header class="page-heading"><h2 id="dialog-title" tabindex="-1">{{ title }}</h2><button type="button" class="safe-focus" @click="emit('close')">关闭</button></header>
    <slot />
  </dialog>
</template>
