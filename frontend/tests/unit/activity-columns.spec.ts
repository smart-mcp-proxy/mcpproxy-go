import { describe, expect, it } from 'vitest'
import { activityColumnClasses } from '@/utils/activityColumns'

describe('activityColumnClasses', () => {
  it('keeps the usual breakpoints without a Scope column', () => {
    expect(activityColumnClasses(false)).toEqual({
      type: 'hidden sm:table-cell',
      typeGlyph: 'sm:hidden',
      duration: 'hidden md:table-cell',
    })
  })

  it('folds Type and Duration only between lg and xl when Scope shows', () => {
    const c = activityColumnClasses(true)
    // Below lg Scope is hidden, so Type (sm+) and Duration (md+) still show.
    expect(c.type).toContain('sm:table-cell')
    expect(c.duration).toContain('md:table-cell')
    // lg..xl: folded beside Scope; xl+: back.
    expect(c.type).toContain('lg:hidden')
    expect(c.type).toContain('xl:table-cell')
    expect(c.duration).toContain('lg:hidden')
    expect(c.duration).toContain('xl:table-cell')
    // The glyph stands in exactly where Type is hidden: phones and lg..xl.
    expect(c.typeGlyph).toBe('sm:hidden lg:inline xl:hidden')
  })
})
