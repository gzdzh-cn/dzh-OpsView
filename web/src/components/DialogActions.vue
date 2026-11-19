<script setup lang="ts">
import { inject } from 'vue'
import { dialogActionsKey } from '../lib/dialog'

const target = inject(dialogActionsKey)
if (!target) throw new Error('DialogActions must be rendered inside AppDialog')
</script>

<template>
  <Teleport v-if="target" :to="target">
    <div class="ops-dialog-actions"><slot /></div>
  </Teleport>
</template>
