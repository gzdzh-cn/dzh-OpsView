<script setup lang="ts">
import type { Config } from '../lib/api'
const rule = defineModel<Config>({ required: true })
defineProps<{ index: number }>()
const emit = defineEmits(['remove'])
</script>
<template>
  <div class="site-rule-block">
    <div class="page-toolbar">
      <strong>规则 {{ index + 1 }}</strong>
      <el-button link type="danger" @click="emit('remove')">删除规则</el-button>
    </div>
    <el-form-item label="来源路径">
      <el-input v-model="rule.path" placeholder="/old/" />
    </el-form-item>
    <el-form-item label="匹配方式">
      <el-select v-model="rule.exact">
        <el-option :value="false" label="目录前缀" />
        <el-option :value="true" label="完整路径" />
      </el-select>
    </el-form-item>
    <el-form-item label="目标地址">
      <el-input v-model="rule.target" placeholder="https://example.com/new/" />
    </el-form-item>
    <el-form-item label="状态码">
      <el-select v-model="rule.code">
        <el-option
          v-for="code in [301, 302, 307, 308]"
          :key="code"
          :value="code"
          :label="String(code)"
        />
      </el-select>
    </el-form-item>
  </div>
</template>
