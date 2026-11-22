<script setup lang="ts">
withDefaults(defineProps<{ busy?: boolean; ready?: boolean }>(), { ready: true })
</script>
<template>
  <div
    class="loading-content"
    :aria-busy="Boolean(busy)"
    v-loading="busy"
    element-loading-text="正在加载…"
  >
    <slot v-if="!busy && ready" />
  </div>
</template>
