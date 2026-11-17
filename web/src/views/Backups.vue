<script setup lang="ts">
import { usePageState } from '../composables/pageState'
import Resources from './Resources.vue'
const tab = usePageState('tab', 'backup')
</script>
<template>
  <div class="notice">
    备份默认加密，并校验完整性。恢复进入新目录或新数据库；请同时妥善保管服务器上的独立加密密钥。
  </div>
  <el-tabs v-model="tab" class="panel-tabs">
    <el-tab-pane label="备份任务" name="backup" />
    <el-tab-pane label="计划任务" name="schedule" />
  </el-tabs>
  <Resources :key="tab" :kind="tab" embedded />
</template>
