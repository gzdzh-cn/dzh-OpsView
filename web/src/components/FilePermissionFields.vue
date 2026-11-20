<script setup lang="ts">
import { computed } from 'vue'
import type { Config } from '../lib/api'
const value = defineModel<Config>({ required: true })
const props = defineProps<{ rows: Config[]; identities: Config }>()
const columns = [
  { label: '所有者', shift: 6 },
  { label: '用户组', shift: 3 },
  { label: '其他用户', shift: 0 },
]
const actions = [
  { label: '读取', bit: 4 },
  { label: '写入', bit: 2 },
  { label: '执行', bit: 1 },
]
function original(bit: number): boolean | null {
  const states = props.rows.map((r) => (parseInt(r.mode || '0', 8) & bit) !== 0)
  return states.every((v) => v === states[0]) ? states[0]! : null
}
function checked(bit: number): boolean | null {
  return (value.value.permissionMask & bit) !== 0
    ? (value.value.permissionValue & bit) !== 0
    : original(bit)
}
function toggle(bit: number, on: boolean) {
  delete value.value.modeDraft
  value.value.permissionMask |= bit
  if (on) value.value.permissionValue |= bit
  else value.value.permissionValue &= ~bit
}
const octal = computed(() => {
  let n = 0
  for (let bit = 1; bit <= 256; bit *= 2) {
    const v = checked(bit)
    if (v === null) return ''
    if (v) n |= bit
  }
  return n.toString(8).padStart(3, '0')
})
function setOctal(v: string) {
  value.value.modeDraft = v
  if (/^[0]?[0-7]{3}$/.test(v)) {
    value.value.permissionMask = 511
    value.value.permissionValue = parseInt(v, 8)
  }
}
</script>
<template>
  <div class="file-permission-matrix" role="group" aria-label="读写执行权限">
    <section v-for="column in columns" :key="column.shift">
      <strong>{{ column.label }}</strong>
      <el-checkbox
        v-for="action in actions"
        :key="action.bit"
        :model-value="checked(action.bit << column.shift) === true"
        :indeterminate="checked(action.bit << column.shift) === null"
        :aria-label="column.label + ' ' + action.label"
        @change="(v: unknown) => toggle(action.bit << column.shift, !!v)"
      >
        {{ action.label }}
      </el-checkbox>
    </section>
  </div>
  <el-form label-width="90px">
    <el-form-item label="数字权限">
      <el-input
        :model-value="value.modeDraft ?? octal"
        placeholder="保持混合权限"
        maxlength="4"
        @input="setOctal"
      />
      <small
        v-if="value.modeDraft !== undefined && !/^[0]?[0-7]{3}$/.test(value.modeDraft)"
        class="error"
      >
        请输入 000–777
      </small>
    </el-form-item>
    <el-form-item label="所有者">
      <el-select
        v-model="value.owner"
        filterable
        clearable
        :placeholder="rows.length === 1 ? '当前 ' + rows[0]?.owner + '，保持不变' : '保持不变'"
      >
        <el-option
          v-for="v in identities.users || []"
          :key="v.id"
          :label="v.name + ' (' + v.id + ')'"
          :value="v.name"
        />
      </el-select>
    </el-form-item>
    <el-form-item label="用户组">
      <el-select
        v-model="value.group"
        filterable
        clearable
        :placeholder="rows.length === 1 ? '当前 ' + rows[0]?.group + '，保持不变' : '保持不变'"
      >
        <el-option
          v-for="v in identities.groups || []"
          :key="v.id"
          :label="v.name + ' (' + v.id + ')'"
          :value="v.name"
        />
      </el-select>
    </el-form-item>
    <el-form-item label="应用范围">
      <el-checkbox v-model="value.recursive">应用到子目录和文件</el-checkbox>
    </el-form-item>
  </el-form>
</template>
<style scoped>
.file-permission-matrix {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  padding: 12px;
  border: 1px solid var(--el-border-color);
  border-radius: var(--el-border-radius-base);
  margin-bottom: 16px;
}
.file-permission-matrix section {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.file-permission-matrix strong {
  margin-bottom: 8px;
  font-size: 13px;
}
.file-permission-matrix .el-checkbox {
  margin: 0;
}
.el-select {
  width: 100%;
}
.error {
  color: var(--el-color-danger);
}
</style>
