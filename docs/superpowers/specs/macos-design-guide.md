# MCPProxy macOS App Design Guide

## Status: Draft (2026-03-27)

Based on research of Apple HIG, best macOS tray apps (Raycast, Bartender, iStatMenus, Docker Desktop), and audit of current MCPProxy Swift code.

---

## Core Principles

1. **Look native, not web** — Use system colors, fonts, spacing. Never fight macOS conventions.
2. **Adapt automatically** — Dark mode, high contrast, accent colors should "just work" via semantic colors.
3. **Accessible by default** — VoiceOver labels, keyboard navigation, no color-only indicators.
4. **Consistent** — One spacing grid, one color palette, one button style hierarchy.

---

## Color Rules

### DO: Use Semantic Colors

| Purpose | SwiftUI | AppKit |
|---------|---------|--------|
| Primary text | `.primary` | `.labelColor` |
| Secondary text | `.secondary` | `.secondaryLabelColor` |
| Tertiary text | `.tertiary` | `.tertiaryLabelColor` |
| Background | default (no color) | `.windowBackgroundColor` |
| Card background | `Color(.controlBackgroundColor)` | `.controlBackgroundColor` |
| Separator | `.separator` | `.separatorColor` |
| Accent/selection | `.accentColor` | `.controlAccentColor` |

### DON'T: Hardcode Colors

```swift
// BAD — breaks in dark mode / high contrast
.foregroundStyle(.white)
.background(Color.red)
.foregroundColor(.green)

// GOOD — adapts automatically
.foregroundStyle(.primary)
.background(.red.opacity(0.15))
.foregroundStyle(.green)  // OK for status if combined with text/icon
```

### Status Colors (Centralized)

Define ONCE, use everywhere:

```swift
extension Color {
    static func healthColor(_ level: String) -> Color {
        switch level {
        case "healthy": return .green
        case "degraded": return .yellow
        case "unhealthy": return .red
        default: return .gray
        }
    }
}
```

Always combine color with a **secondary indicator** (text label, icon shape, or pattern) for colorblind users.

---

## Typography Rules

### DO: Use Text Styles (Dynamic Type)

| Purpose | Text Style | Approx Size |
|---------|-----------|-------------|
| Page title | `.title2` | 22pt |
| Section header | `.headline` | 13pt bold |
| Body text | `.body` | 13pt |
| Table cell primary | `.body` | 13pt |
| Table cell secondary | `.subheadline` | 11pt |
| Badges/labels | `.caption` | 10pt |
| Tiny metadata | `.caption2` | 9pt |
| Monospaced (logs, IDs) | `.body.monospaced()` | 13pt |

### DON'T: Hardcode Sizes

```swift
// BAD
.font(.system(size: 28, weight: .bold))
.font(.system(size: 11))

// GOOD
.font(.title)
.font(.subheadline)
```

Exception: NSTableView cells (AppKit) must use explicit sizes — use `NSFont.systemFont(ofSize: NSFont.systemFontSize)` (13pt default).

---

## Spacing Grid (8pt base)

| Token | Value | Usage |
|-------|-------|-------|
| `xxs` | 4pt | Icon-to-text gap, dense rows |
| `xs` | 8pt | Between related items |
| `sm` | 12pt | Section internal padding |
| `md` | 16pt | Card padding, section gap |
| `lg` | 20pt | Page padding |
| `xl` | 24pt | Major section separation |

### Standard Padding

```swift
// Page content
.padding(20)

// Cards
.padding(16)

// Between sections
VStack(spacing: 20) { ... }

// Between items in a section
VStack(spacing: 8) { ... }
```

### Corner Radius

Standardize to **8pt** everywhere. Exception: small badges use **4pt**.

---

## Sidebar Design

### Current (Non-Standard)
- Colored rectangle badges behind icons
- Custom font sizes

### Correct macOS Pattern
- **Monochrome SF Symbols** (no colored backgrounds)
- System sidebar list style
- Selected item uses `.accentColor` highlight
- Font: system default (let NavigationSplitView handle it)

```swift
// GOOD
Label(item.rawValue, systemImage: item.icon)
    .tag(item)

// BAD — custom colored icon badges
Image(systemName: item.icon)
    .foregroundStyle(.white)
    .background(item.color)
    .clipShape(RoundedRectangle(cornerRadius: 6))
```

---

## Table Design

### NSTableView (Servers)

| Property | Value | Rationale |
|----------|-------|-----------|
| Row height | 28pt | macOS standard for spacious tables |
| Alternating rows | Yes | Aids readability |
| Column headers | Yes | Clickable for sorting |
| Intercell spacing | 12pt horizontal | Standard macOS |
| Font (primary) | 13pt system | `.systemFontSize` |
| Font (secondary) | 11pt system | `.smallSystemFontSize` |
| Status dot | 8pt circle | Visible but not dominant |

### SwiftUI Tables (Dashboard, Activity Log)

Use `HStack` with `.frame(width:alignment:)` for column alignment. Consistent column widths across similar views.

---

## Status Indicators

### Health Dots
- **Size**: 8pt (tables), 12pt (headers/detail views)
- **Colors**: green (healthy), yellow (degraded), red (unhealthy), gray (disabled/disconnected), orange (quarantined)
- **Always** accompanied by text label (for colorblind users)

### Badges

```swift
// Standard badge pattern
Text(status)
    .font(.caption2)
    .fontWeight(.semibold)
    .padding(.horizontal, 8)
    .padding(.vertical, 3)
    .background(color.opacity(0.15))
    .foregroundStyle(color)
    .clipShape(Capsule())
```

---

## Tray Menu Design

### Icon
- **Template image** (monochrome, `isTemplate = true`)
- macOS auto-adapts to light/dark menu bar
- Size: 18x18pt (@1x)
- Optional: status dot overlay (tiny colored circle at bottom-right)

### Menu Structure
```
MCPProxy vX.Y.Z
{server count} servers, {tool count} tools
─────────────────
⚠ Needs Attention (N)     [if any]
  server — action needed
─────────────────
Servers (N)              ▶
Add Server...            ⌘N
─────────────────
Open MCPProxy...         ⌘O
Open Web UI
─────────────────
Run at Startup
Check for Updates
─────────────────
Pause/Resume Core
Quit MCPProxy            ⌘Q
```

### Menu Rules
- **Max 2 levels** of submenu nesting
- Font: system 11pt (let macOS control)
- Separator between logical groups
- Keyboard shortcuts for common actions
- Disabled items at 35% opacity

---

## Accessibility Checklist

- [ ] All interactive controls have `.accessibilityLabel()`
- [ ] Status colors combined with text labels
- [ ] VoiceOver can navigate all table cells
- [ ] Keyboard navigation works (Tab, Arrow keys)
- [ ] No white-on-red or green-on-black without contrast backup
- [ ] `.accessibilityIdentifier()` for UI testing
- [ ] Dynamic Type support (or scaleEffect zoom)

---

## Current Issues (Prioritized Fix List)

### Phase 1: Critical (Colors + Accessibility)
1. Centralize status colors in one extension (used in 4+ files)
2. Replace `.foregroundStyle(.white).background(.red)` with accessible patterns
3. Add `.accessibilityLabel()` to all buttons, badges, status dots
4. Fix `.opacity(0.5)` backgrounds (invisible in dark mode)

### Phase 2: Typography + Spacing
5. Replace hardcoded font sizes with text styles
6. Standardize padding to 8/16/20/24pt grid
7. Standardize corner radius to 8pt
8. Fix inconsistent button `.controlSize`

### Phase 3: Sidebar + Layout
9. Remove colored icon badges from sidebar
10. Reduce table row height from 36pt to 28pt
11. Create reusable `EmptyStateView` component
12. Extract duplicate color/badge definitions

### Phase 4: Polish
13. Add localization support (String(localized:))
14. Add keyboard shortcuts (Cmd+E edit, Cmd+R refresh)
15. Improve JSON syntax highlighting colors for dark mode
16. Add drag reordering support to server table

---

## Reference Apps

| App | What to Learn |
|-----|---------------|
| Docker Desktop | Table with action buttons, status indicators |
| Xcode | Sidebar + detail navigation |
| Activity Monitor | Column sorting, real-time data |
| System Settings | Sidebar navigation, consistent spacing |
| Bartender | Menu bar icon design, menu structure |
| Raycast | Keyboard-first, instant response |
