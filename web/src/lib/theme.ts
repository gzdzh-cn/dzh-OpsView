// Shared palette for navigation, Element Plus, overlays, editors and charts.
// User colors are normalized for readable text in both modes.
type RGB = [number, number, number]
export const defaultPrimary = '#2563eb'
const rgb = (hex: string): RGB => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16)) as RGB
const hex = (channels: RGB) =>
  '#' + channels.map((v) => Math.round(v).toString(16).padStart(2, '0')).join('')
export function mix(color: string, background: string, ratio: number) {
  const a = rgb(color),
    b = rgb(background)
  return hex(a.map((v, i) => v + (b[i]! - v) * ratio) as RGB)
}
function luminance(color: string) {
  return rgb(color).reduce((sum, channel, i) => {
    const s = channel / 255
    return (
      sum + (s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4) * [0.2126, 0.7152, 0.0722][i]!
    )
  }, 0)
}
export function contrast(a: string, b: string) {
  const x = luminance(a),
    y = luminance(b)
  return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05)
}
function readable(color: string, backgrounds: string[], dark: boolean) {
  const target = dark ? '#ffffff' : '#000000'
  for (let step = 0; step <= 100; step++) {
    const candidate = mix(color, target, step / 100)
    const tinted = mix(candidate, backgrounds[0]!, dark ? 0.78 : 0.9)
    if ([...backgrounds, tinted].every((background) => contrast(candidate, background) >= 4.5))
      return candidate
  }
  return target
}
export function createTheme(dark: boolean, primary: unknown = defaultPrimary) {
  const colors: Record<string, string> = dark
    ? {
        '--ops-page': '#101722',
        '--ops-surface': '#182230',
        '--ops-raised': '#202d3d',
        '--ops-subtle': '#1d2938',
        '--ops-hover': '#263548',
        '--ops-border': '#3b4b60',
        '--ops-border-soft': '#2d3c4f',
        '--ops-text': '#e5edf7',
        '--ops-text-regular': '#c3cfe0',
        '--ops-text-muted': '#9eafc6',
        '--ops-text-placeholder': '#91a3bc',
        '--ops-text-disabled': '#708199',
        '--ops-sidebar': '#131c29',
        '--ops-sidebar-text': '#b6c5d9',
        '--ops-sidebar-border': '#2d3c4f',
        '--ops-terminal': '#0c131e',
        '--ops-terminal-text': '#d2def0',
        '--ops-shadow': '0 12px 36px #00000040',
        '--ops-mask': '#101722cc',
      }
    : {
        '--ops-page': '#f3f5f9',
        '--ops-surface': '#ffffff',
        '--ops-raised': '#ffffff',
        '--ops-subtle': '#f6f8fb',
        '--ops-hover': '#edf1f7',
        '--ops-border': '#d1d9e5',
        '--ops-border-soft': '#e3e8f0',
        '--ops-text': '#1e293b',
        '--ops-text-regular': '#475569',
        '--ops-text-muted': '#64748b',
        '--ops-text-placeholder': '#6b7b91',
        '--ops-text-disabled': '#98a5b6',
        '--ops-sidebar': '#e9eef5',
        '--ops-sidebar-text': '#475569',
        '--ops-sidebar-border': '#d1d9e5',
        '--ops-terminal': '#172234',
        '--ops-terminal-text': '#d5e1f1',
        '--ops-shadow': '0 12px 36px #1722341a',
        '--ops-mask': '#ffffffcc',
      }
  const selected =
    typeof primary === 'string' && /^#[\da-f]{6}$/i.test(primary) ? primary : defaultPrimary
  const bases = {
    primary: selected,
    success: dark ? '#53d5a0' : '#16805d',
    warning: dark ? '#f2ba60' : '#9a650b',
    danger: dark ? '#ff929c' : '#c9364e',
    info: dark ? '#a9b9ce' : '#63748b',
  }
  const backgrounds = [
    colors['--ops-surface']!,
    colors['--ops-raised']!,
    colors['--ops-subtle']!,
    colors['--ops-hover']!,
  ]
  for (const [name, base] of Object.entries(bases)) {
    const value = readable(base, backgrounds, dark)
    colors[`--el-color-${name}`] = value
    colors[`--el-color-${name}-rgb`] = rgb(value).join(', ')
    colors[`--ops-on-${name}`] = contrast(value, '#ffffff') >= 4.5 ? '#ffffff' : '#101722'
    for (let step = 1; step <= 9; step++) {
      colors[`--el-color-${name}-light-${step}`] = mix(value, colors['--ops-surface']!, step / 10)
    }
    colors[`--el-color-${name}-dark-2`] = mix(value, dark ? '#ffffff' : '#000000', 0.16)
  }
  for (const [name, value] of Object.entries(colors)) {
    if (name.startsWith('--el-color-danger')) colors[name.replace('danger', 'error')] = value
  }
  colors['--ops-selection'] = mix(
    colors['--el-color-primary']!,
    colors['--ops-surface']!,
    dark ? 0.78 : 0.9,
  )
  colors['--ops-chart-secondary'] = dark ? '#a89afa' : '#7c5bd6'
  return { dark, colors }
}
