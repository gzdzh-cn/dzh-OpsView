<script setup lang="ts">
import type { Config } from '../lib/api'
const rule = defineModel<Config>({ required: true })
defineProps<{ index: number }>()
const emit = defineEmits(['remove'])
function targets(value: string) {
  rule.value.targets = value.split(/\s+/).filter(Boolean)
}
</script>
<template>
  <div class="proxy-route">
    <div class="route-heading">
      <strong>路径规则 {{ index + 1 }}</strong>
      <el-button link type="danger" @click="emit('remove')">移除规则</el-button>
    </div>
    <el-form-item label="路径前缀">
      <el-input v-model="rule.path" placeholder="/ 或 /api/" />
    </el-form-item>
    <el-form-item label="上游地址（每行一个）">
      <el-input
        :model-value="(rule.targets || []).join('\n')"
        type="textarea"
        :rows="3"
        placeholder="http://127.0.0.1:3000"
        @update:model-value="targets($event)"
      />
    </el-form-item>
    <el-form-item label="请求 Host">
      <el-input v-model="rule.host" placeholder="$host 或 api.example.com" />
    </el-form-item>
    <el-form-item label="WebSocket"><el-switch v-model="rule.websocket" /></el-form-item>
    <div class="route-timeouts">
      <el-form-item label="连接超时（秒）">
        <el-input-number v-model="rule.connectTimeout" :min="1" :max="3600" />
      </el-form-item>
      <el-form-item label="读取超时（秒）">
        <el-input-number v-model="rule.readTimeout" :min="1" :max="3600" />
      </el-form-item>
      <el-form-item label="发送超时（秒）">
        <el-input-number v-model="rule.sendTimeout" :min="1" :max="3600" />
      </el-form-item>
    </div>
  </div>
</template>
<style scoped>
.proxy-route {
  border: 1px solid #e2e8f0;
  border-radius: 6px;
  padding: 16px;
  margin-bottom: 16px;
}
.route-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.route-timeouts {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
}
</style>
