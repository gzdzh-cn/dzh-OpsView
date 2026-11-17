<script setup lang="ts">
import { usePageScroll } from '../composables/pageState'
usePageScroll()
</script>
<template>
  <div class="page-content"><slot /></div>
</template>
