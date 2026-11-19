import { describe, expect, it } from 'vitest'
import { contrast, createTheme, defaultPrimary } from './theme'

describe('shared theme palette', () => {
  for (const dark of [false, true]) {
    it(`${dark ? 'dark' : 'light'} keeps custom accents and semantic actions readable`, () => {
      for (const primary of [
        defaultPrimary,
        '#409eff',
        '#ffffff',
        '#000000',
        '#ffff00',
        '#00ff00',
        '#ff0000',
        '#663399',
      ]) {
        const { colors } = createTheme(dark, primary)
        for (const name of ['primary', 'success', 'warning', 'danger', 'info']) {
          const accent = colors[`--el-color-${name}`]!
          for (const background of ['--ops-surface', '--ops-raised', '--ops-subtle', '--ops-hover'])
            expect(contrast(accent, colors[background]!)).toBeGreaterThanOrEqual(4.5)
          expect(contrast(accent, colors[`--ops-on-${name}`]!)).toBeGreaterThanOrEqual(4.5)
          expect(
            contrast(colors[`--el-color-${name}-dark-2`]!, colors[`--ops-on-${name}`]!),
          ).toBeGreaterThanOrEqual(4.5)
          expect(contrast(accent, colors[`--el-color-${name}-light-9`]!)).toBeGreaterThanOrEqual(
            4.5,
          )
        }
        expect(
          contrast(colors['--el-color-primary']!, colors['--ops-selection']!),
        ).toBeGreaterThanOrEqual(4.5)
        for (const text of ['--ops-text', '--ops-text-regular', '--ops-text-muted'])
          expect(contrast(colors[text]!, colors['--ops-surface']!)).toBeGreaterThanOrEqual(4.5)
      }
    })
    it(`${dark ? 'dark' : 'light'} mixes muted fills toward its own surface`, () => {
      const { colors } = createTheme(dark)
      const fill = colors['--el-color-primary-light-9']!
      expect(contrast(fill, colors['--ops-surface']!)).toBeLessThan(1.6)
      if (dark) expect(contrast(fill, '#ffffff')).toBeGreaterThan(8)
    })
  }
  it('rejects malformed imported colors without emitting invalid CSS', () => {
    for (const invalid of [null, '', '#fff', '#gggggg', 'red;display:none', {}])
      expect(createTheme(false, invalid)).toEqual(createTheme(false, defaultPrimary))
  })
  it('shares the error palette with danger for alerts, messages and validation', () => {
    for (const dark of [false, true]) {
      const { colors } = createTheme(dark)
      for (const suffix of [
        '',
        '-rgb',
        '-light-3',
        '-light-5',
        '-light-7',
        '-light-8',
        '-light-9',
        '-dark-2',
      ])
        expect(colors['--el-color-error' + suffix]).toBe(colors['--el-color-danger' + suffix])
    }
  })
})
