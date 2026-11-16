<script setup lang="ts">
import { computed } from 'vue'
import { ICON_PATHS } from '../lib/icons'
const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 18 })
const path = computed(() => ICON_PATHS[props.name] ?? ICON_PATHS.file)
</script>
<template><svg class="icon" :width="size" :height="size" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path :d="path" /></svg></template>
