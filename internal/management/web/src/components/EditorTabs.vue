<script setup lang="ts">
import { nextTick } from 'vue'
defineProps<{ id: string; label: string; selected: string; items: { id: string; label: string; panel: string; errors?: number }[] }>()
const emit = defineEmits<{ select: [string] }>()
async function navigate(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const buttons = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role="tab"]')]
  const current = buttons.indexOf(event.target as HTMLButtonElement)
  if (current < 0) return
  event.preventDefault()
  const index = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length
  const button = buttons[index]!
  emit('select', button.dataset.section!)
  await nextTick()
  button.focus({ preventScroll: true })
}
</script>
<template>
  <div class="editor-tabs" role="tablist" :aria-label="label" @keydown="navigate">
    <button v-for="item in items" :id="id + '-' + item.id" :key="item.id" :data-section="item.id" type="button" role="tab" :tabindex="selected === item.id ? 0 : -1" :aria-selected="selected === item.id" :aria-controls="item.panel" @click="emit('select', item.id)">{{ item.label }}<span v-if="item.errors" class="editor-error-count" :aria-label="item.errors + ' 项错误'">{{ item.errors }}</span></button>
  </div>
</template>
