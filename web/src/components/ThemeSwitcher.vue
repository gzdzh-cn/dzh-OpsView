<script setup lang="ts">
import { ref, computed } from 'vue'
import { Monitor, Sunny, Moon } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { api, errorText } from '../lib/api'
import { appearance, applyAppearance } from '../lib/appearance'
const busy = ref(false)
const choices = [
  { value: 'auto', label: '跟随系统', icon: Monitor },
  { value: 'light', label: '亮色', icon: Sunny },
  { value: 'dark', label: '黑色', icon: Moon },
]
const current = computed(() => choices.find((c) => c.value === appearance.theme) || choices[1]!)
async function change(theme: string) {
  if (busy.value || theme === appearance.theme) return
  busy.value = true
  try {
    const base = await api('/settings/groups/appearance')
    const result = await api('/settings/groups/appearance', {
      method: 'PATCH',
      body: JSON.stringify({ revision: base.revision, patch: { theme } }),
    })
    applyAppearance({ ...base.config, ...result.config, theme })
    window.dispatchEvent(new CustomEvent('opsview:preferences', { detail: { theme } }))
  } catch (e) {
    ElMessage.error(errorText(e))
  } finally {
    busy.value = false
  }
}
</script>
<template>
  <el-dropdown trigger="click" @command="change">
    <button
      class="icon-button theme-switcher"
      :disabled="busy"
      :aria-busy="busy"
      :title="'主题：' + current.label"
      aria-label="切换主题"
    >
      <el-icon :class="{ 'is-loading': busy }"><component :is="current.icon" /></el-icon>
    </button>
    <template #dropdown>
      <el-dropdown-menu aria-label="主题模式">
        <el-dropdown-item
          v-for="c in choices"
          :key="c.value"
          :command="c.value"
          :disabled="busy"
          :icon="c.icon"
          :class="{ 'theme-selected': appearance.theme === c.value }"
        >
          {{ c.label }}
          <span v-if="appearance.theme === c.value" aria-label="已选择">✓</span>
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>
