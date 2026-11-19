import { reactive } from 'vue'
import type { Config } from './api'
export const appearance = reactive<Config>({
  name: 'OpsView',
  sidebarTitle: 'OpsView',
  theme: 'auto',
  primaryColor: '#409eff',
  fontSize: 14,
  hiddenMenus: [],
})
const media = window.matchMedia('(prefers-color-scheme: dark)')
function render() {
  const dark = appearance.theme === 'dark' || (appearance.theme === 'auto' && media.matches)
  document.documentElement.classList.toggle('dark', dark)
  document.documentElement.style.setProperty(
    '--el-color-primary',
    appearance.primaryColor || '#409eff',
  )
  document.documentElement.style.setProperty('--ops-font-size', (appearance.fontSize || 14) + 'px')
  const color = String(appearance.primaryColor || '#409eff'),
    rgb = [1, 3, 5].map((i) => parseInt(color.slice(i, i + 2), 16))
  for (let n = 1; n <= 9; n++) {
    const ratio = n / 10
    document.documentElement.style.setProperty(
      '--el-color-primary-light-' + n,
      '#' +
        rgb
          .map((c) =>
            Math.round(c + (255 - c) * ratio)
              .toString(16)
              .padStart(2, '0'),
          )
          .join(''),
    )
  }
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
export function applyAppearance(config: Config) {
  Object.assign(appearance, config)
  render()
}
