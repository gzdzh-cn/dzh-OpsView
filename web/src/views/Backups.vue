<script setup lang="ts">
import { useRoute } from 'vue-router'
import { usePageState } from '../composables/pageState'
import ScheduleManagement from '../components/ScheduleManagement.vue'
import BackupRecords from '../components/BackupRecords.vue'
import ScriptLibrary from '../components/ScriptLibrary.vue'
const route = useRoute(),
  tab = usePageState('tab', route.query.tab === 'records' ? 'records' : 'schedule')
</script>
<template>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="计划任务" name="schedule" />
    <el-tab-pane label="备份记录" name="records" />
    <el-tab-pane label="脚本库" name="scripts" />
  </el-tabs>
  <ScheduleManagement v-if="tab === 'schedule'" />
  <BackupRecords v-else-if="tab === 'records'" />
  <ScriptLibrary v-else />
</template>
