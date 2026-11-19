<script setup lang="ts">
import { ref, watch } from 'vue'
const model = defineModel<string>({ required: true })
const mode = ref('custom'),
  hour = ref(3),
  minute = ref(0),
  weekday = ref(1),
  day = ref(1),
  interval = ref(30)
let syncing = false
watch(
  model,
  (value) => {
    syncing = true
    const p = value.trim().split(/\s+/)
    if (/^\*\/\d+ \* \* \* \*$/.test(value)) {
      mode.value = 'interval'
      interval.value = +p[0]!.slice(2)
    } else if (/^\d+ \d+ \* \* \*$/.test(value)) {
      mode.value = 'daily'
      minute.value = +p[0]!
      hour.value = +p[1]!
    } else if (/^\d+ \d+ \* \* [0-6]$/.test(value)) {
      mode.value = 'weekly'
      minute.value = +p[0]!
      hour.value = +p[1]!
      weekday.value = +p[4]!
    } else if (/^\d+ \d+ \d+ \* \*$/.test(value)) {
      mode.value = 'monthly'
      minute.value = +p[0]!
      hour.value = +p[1]!
      day.value = +p[2]!
    } else mode.value = 'custom'
    syncing = false
  },
  { immediate: true, flush: 'sync' },
)
function update() {
  if (syncing || mode.value === 'custom') return
  model.value =
    mode.value === 'interval'
      ? `*/${interval.value} * * * *`
      : `${minute.value} ${hour.value} ${mode.value === 'monthly' ? day.value : '*'} * ${mode.value === 'weekly' ? weekday.value : '*'}`
}
</script>
<template>
  <div class="schedule-cycle">
    <el-select v-model="mode" aria-label="执行周期" @change="update">
      <el-option label="每天" value="daily" />
      <el-option label="每周" value="weekly" />
      <el-option label="每月" value="monthly" />
      <el-option label="每隔 N 分钟" value="interval" />
      <el-option label="自定义 Cron" value="custom" />
    </el-select>
    <template v-if="mode === 'interval'">
      <el-input-number
        v-model="interval"
        aria-label="间隔分钟"
        :min="1"
        :max="59"
        @change="update"
      />
      <span>分钟</span>
    </template>
    <template v-else-if="mode !== 'custom'">
      <el-select v-if="mode === 'weekly'" v-model="weekday" aria-label="星期" @change="update">
        <el-option
          v-for="(label, i) in ['日', '一', '二', '三', '四', '五', '六']"
          :key="i"
          :value="i"
          :label="'周' + label"
        />
      </el-select>
      <template v-if="mode === 'monthly'">
        <el-input-number v-model="day" aria-label="每月日期" :min="1" :max="28" @change="update" />
        <span>日</span>
      </template>
      <el-input-number v-model="hour" aria-label="小时" :min="0" :max="23" @change="update" />
      <span>时</span>
      <el-input-number v-model="minute" aria-label="分钟" :min="0" :max="59" @change="update" />
      <span>分</span>
    </template>
    <el-input v-model="model" aria-label="Cron 表达式" placeholder="0 3 * * *" />
  </div>
</template>
