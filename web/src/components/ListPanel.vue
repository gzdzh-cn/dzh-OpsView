<script setup lang="ts">
import LoadingContent from './LoadingContent.vue'
withDefaults(
  defineProps<{
    title?: string
    count?: number
    busy?: boolean
    error?: string
    ready?: boolean
  }>(),
  { ready: true },
)
defineEmits(['retry'])
</script>
<template>
  <section class="surface list-panel" :aria-busy="busy">
    <div v-if="title || $slots.toolbar || $slots.actions" class="page-toolbar">
      <div class="page-toolbar-left">
        <h3 v-if="title">{{ title }}</h3>
        <span v-if="count !== undefined" class="count-badge">{{ count }}</span>
        <slot name="toolbar" />
      </div>
      <div class="page-toolbar-right"><slot name="actions" /></div>
    </div>
    <el-alert v-if="error" :title="error" type="error" :closable="false" class="list-error">
      <template #default><el-button link @click="$emit('retry')">重新加载</el-button></template>
    </el-alert>
    <slot name="navigation" />
    <LoadingContent :busy="busy" :ready="ready" class="list-panel-body"><slot /></LoadingContent>
    <slot name="footer" />
  </section>
</template>
