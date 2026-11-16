<script setup lang="ts">
defineProps<{ title: string; desc?: string }>()
</script>
<template><header class="page-heading page-head"><div class="page-head-main"><h1 tabindex="-1">{{ title }}</h1><p v-if="desc" class="muted">{{ desc }}</p></div><div v-if="$slots.default" class="page-actions"><slot /></div></header></template>
