<script setup lang="ts">
defineProps<{ busy?: boolean }>()
</script>
<template>
  <div
    class="loading-content"
    :aria-busy="Boolean(busy)"
    v-loading="busy"
    element-loading-text="正在加载…"
  >
    <slot v-if="!busy" />
  </div>
</template>
