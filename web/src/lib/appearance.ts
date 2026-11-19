import { reactive, shallowRef } from 'vue'
import type { Config } from './api'
import { createTheme, defaultPrimary } from './theme'
export const appearance = reactive<Config>({
  name: 'OpsView',
  sidebarTitle: 'OpsView',
  theme: 'light',
  primaryColor: defaultPrimary,
  fontSize: 14,
  hiddenMenus: [],
})
const media = window.matchMedia('(prefers-color-scheme: dark)')
export const resolvedTheme = shallowRef(createTheme(false))
function render() {
  const dark = appearance.theme === 'dark' || (appearance.theme === 'auto' && media.matches)
  const palette = createTheme(dark, appearance.primaryColor)
  document.documentElement.classList.toggle('dark', dark)
  for (const [name, value] of Object.entries(palette.colors))
    document.documentElement.style.setProperty(name, value)
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute('content', palette.colors['--ops-page']!)
  document.documentElement.style.setProperty('--ops-font-size', (appearance.fontSize || 14) + 'px')
  resolvedTheme.value = palette
  document.title = appearance.name || 'OpsView'
  let icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!icon) {
    icon = document.createElement('link')
    icon.rel = 'icon'
    document.head.append(icon)
  }
  icon.href = appearance.favicon || '/favicon.ico'
}
media.addEventListener('change', render)
render()
export function applyAppearance(config: Config) {
  Object.assign(appearance, config)
  render()
}
