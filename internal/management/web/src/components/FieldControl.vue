<script setup lang="ts">
defineProps<{ id: string; label: string; error?: string; hint?: string }>()
</script>
<template><div class="field"><label :for="id">{{ label }}</label><slot :id="id" :invalid="!!error" :describedby="error ? id + '-error' : hint ? id + '-hint' : undefined" /><p v-if="hint" :id="id + '-hint'" class="small muted">{{ hint }}</p><p v-if="error" :id="id + '-error'" class="field-error">{{ error }}</p></div></template>
