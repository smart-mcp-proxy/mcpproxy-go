// Responsive classes for the Activity table's Type and Duration columns.
//
// The Scope column shows from lg (1024px) and costs ~176px. Between lg and xl
// the card cannot also hold Type and Duration, so they fold there while Scope
// shows; below lg Scope is hidden, so they keep their usual sm/md breakpoints.
// When Type is folded, Details shows the type glyph instead (as on phones).
export interface ActivityColumnClasses {
  type: string
  typeGlyph: string
  duration: string
}

export function activityColumnClasses(hasScopeColumn: boolean): ActivityColumnClasses {
  if (!hasScopeColumn) {
    return { type: 'hidden sm:table-cell', typeGlyph: 'sm:hidden', duration: 'hidden md:table-cell' }
  }
  return {
    type: 'hidden sm:table-cell lg:hidden xl:table-cell',
    typeGlyph: 'sm:hidden lg:inline xl:hidden',
    duration: 'hidden md:table-cell lg:hidden xl:table-cell',
  }
}
