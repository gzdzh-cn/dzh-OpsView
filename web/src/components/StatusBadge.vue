<script setup lang="ts">
import { computed } from 'vue'
const props = defineProps<{ value: string }>()
const labels: Record<string, string> = {
  ready: '已应用',
  pending: '待部署',
  running: '执行中',
  queued: '等待中',
  succeeded: '已完成',
  failed: '失败',
  interrupted: '已中断',
  cancelled: '已取消',
  expired: '已到期',
  stopped: '已停止',
  error: '需检查',
  installing: '安装中',
}
const label = computed(() => labels[props.value] || props.value)
</script>
<template>
  <span class="status-badge" :class="value">
    <i></i>
    {{ label }}
  </span>
</template>
