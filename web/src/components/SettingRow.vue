<script setup lang="ts">
defineProps<{ label: string; value?: string | number; help?: string }>()
defineEmits<{ edit: [] }>()
</script>
<template>
  <div class="preference-row">
    <strong>{{ label }}</strong>
    <div class="preference-value">
      <slot>{{ value ?? '—' }}</slot>
      <small v-if="help">{{ help }}</small>
    </div>
    <div class="preference-action">
      <slot name="action">
        <el-button link type="primary" @click="$emit('edit')">修改</el-button>
      </slot>
    </div>
  </div>
</template>
