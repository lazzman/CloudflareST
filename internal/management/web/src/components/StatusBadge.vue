<script setup lang="ts">
import { label, tone } from '../lib/domain'
defineProps<{ value?: string }>()
</script>
<template><span class="badge" :class="tone(value)">{{ label(value) }}</span></template>
