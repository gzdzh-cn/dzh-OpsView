import type { InjectionKey, Ref } from 'vue'

// Each dialog owns its target, including when another dialog opens above it.
export const dialogActionsKey: InjectionKey<Ref<HTMLElement | undefined>> = Symbol('dialog-actions')
