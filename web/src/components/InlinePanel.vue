<script setup lang="ts">
defineProps<{ modelValue: boolean; title?: string }>()
</script>
<template>
  <section v-if="modelValue" class="settings-inline-details" :aria-label="title">
    <slot />
    <div class="settings-inline-actions"><slot name="footer" /></div>
  </section>
</template>
