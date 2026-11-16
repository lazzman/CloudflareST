<script setup lang="ts">
defineProps<{ title?: string; meta?: string }>()
</script>
<template><section class="panel m-surface"><header v-if="title || $slots.tools" class="panel-head"><div class="panel-heading"><h2>{{ title }}</h2><span v-if="meta" class="panel-meta">{{ meta }}</span></div><div v-if="$slots.tools" class="panel-tools"><slot name="tools" /></div></header><slot /></section></template>
