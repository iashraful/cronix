import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const css = readFileSync(new URL('./style.css', import.meta.url), 'utf8')

function checkClass(sel) {
  return css.includes(sel)
}

describe('design system', () => {
  it('defines both theme token blocks', () => {
    expect(css).toContain(':root')
    expect(css).toContain(`[data-theme='dark']`)
  })

  it('defines required semantic roles', () => {
    for (const tok of ['--bg', '--bg-raised', '--text', '--text-muted', '--border-subtle', '--accent', '--ok', '--fail', '--ring', '--radius-md', '--mono']) {
      expect(css, `missing token ${tok}`).toContain(tok)
    }
  })

  it('defines component classes used by the views', () => {
    const classes = [
      '.btn.primary', '.btn.danger', '.stats', '.stat-label', '.tbl', '.pill.ok',
      '.dot.failed', '.toggle.on', '.tabs', '.tab.active', '.run-item', '.banner', '.cmd-block',
    ]
    for (const c of classes) expect(css, `missing class ${c}`).toContain(c)
  })
})
