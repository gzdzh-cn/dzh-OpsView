<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import Resources from './Resources.vue'
import ScheduleManagement from '../components/ScheduleManagement.vue'
const tab = usePageState('tab', 'backup')
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="备份任务" name="backup" />
    <el-tab-pane label="计划任务" name="schedule" />
  </el-tabs>
  <Resources v-if="tab === 'backup'" kind="backup" embedded />
  <ScheduleManagement v-else />
</template>
