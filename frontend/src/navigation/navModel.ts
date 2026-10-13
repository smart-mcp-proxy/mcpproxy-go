/**
 * The one navigation model (Spec 109 FR-050, contracts/navigation-map.md).
 *
 * The sidebar, the header "+ Add" menu and the command palette all read the
 * names, order and targets defined here, so a page cannot be called one thing
 * in the sidebar and another in the palette. Icons are referenced by key; the
 * sidebar owns the actual SVGs.
 *
 * Personal edition only. The server-edition menus (`/my/*`, `/admin/*`) keep
 * their own lists in SidebarNav.
 */
import type { RouteLocationRaw } from 'vue-router'

export type NavIcon =
  | 'home' | 'clients' | 'profiles' | 'servers' | 'tools' | 'review'
  | 'secrets' | 'activity' | 'usage' | 'settings' | 'docs' | 'feedback' | 'theme'

export type NavBadge = 'attention' | 'clients' | 'servers' | 'tools' | 'review' | 'secrets'

export interface NavItem {
  id: string
  label: string
  /** Router path, or an absolute URL when `external` is set. */
  path: string
  icon: NavIcon
  badge?: NavBadge
  external?: boolean
  /**
   * Item is shown only when the router has a route with exactly this path.
   * Profiles ships hidden until Spec 108 registers `/profiles`.
   */
  requiresRoutePath?: string
}

export interface NavGroup {
  id: 'connect' | 'protect' | 'monitor'
  label: string
  items: NavItem[]
}

export const DOCS_URL = 'https://docs.mcpproxy.app'

/** Solo top row, no group label. */
export const SIDEBAR_HOME: NavItem = { id: 'home', label: 'Home', path: '/', icon: 'home', badge: 'attention' }

export const SIDEBAR_GROUPS: NavGroup[] = [
  {
    id: 'connect',
    label: 'Connect',
    items: [
      { id: 'clients', label: 'Clients', path: '/clients', icon: 'clients', badge: 'clients' },
      { id: 'profiles', label: 'Profiles', path: '/profiles', icon: 'profiles', requiresRoutePath: '/profiles' },
      { id: 'servers', label: 'Servers', path: '/servers', icon: 'servers', badge: 'servers' },
      { id: 'tools', label: 'Tools', path: '/tools', icon: 'tools', badge: 'tools' },
    ],
  },
  {
    id: 'protect',
    label: 'Protect',
    items: [
      { id: 'review', label: 'Review queue', path: '/review', icon: 'review', badge: 'review' },
      { id: 'secrets', label: 'Secrets', path: '/secrets', icon: 'secrets', badge: 'secrets' },
    ],
  },
  {
    id: 'monitor',
    label: 'Monitor',
    items: [
      { id: 'activity', label: 'Activity', path: '/activity', icon: 'activity' },
      { id: 'usage', label: 'Usage', path: '/usage', icon: 'usage' },
    ],
  },
]

/** Footer links. Theme is a dropdown, so it is a label only (no path). */
export const SIDEBAR_FOOTER: NavItem[] = [
  { id: 'settings', label: 'Settings', path: '/settings', icon: 'settings' },
  { id: 'docs', label: 'Docs', path: DOCS_URL, icon: 'docs', external: true },
  { id: 'feedback', label: 'Feedback', path: '/feedback', icon: 'feedback' },
]

export const THEME_LABEL = 'Theme'

export interface AddMenuEntry {
  id: 'server' | 'client' | 'token' | 'profile'
  label: string
  /** Navigation target. Absent for `client`, which opens the connect dialog. */
  to?: RouteLocationRaw
  requiresRoutePath?: string
}

/** "+ Add" menu, personal edition. */
export const ADD_MENU_ITEMS: AddMenuEntry[] = [
  { id: 'server', label: 'Server', to: '/add-server' },
  { id: 'client', label: 'Client' },
  { id: 'token', label: 'Token', to: { path: '/clients', query: { tab: 'tokens', create: '1' } } },
  { id: 'profile', label: 'Profile', to: { path: '/profiles', query: { create: '1' } }, requiresRoutePath: '/profiles' },
]

/** Server-edition admin: "+ Add" has a single entry. */
export const ADD_MENU_SERVER_EDITION: AddMenuEntry[] = [
  { id: 'server', label: 'Personal server', to: '/add-server' },
]

/**
 * Window event the palette's "Connect client" action dispatches. AddMenu owns
 * the one connect dialog and opens it on this event.
 */
export const CONNECT_CLIENT_EVENT = 'mcpproxy:connect-client'

export interface PaletteAction {
  id: string
  label: string
  to?: RouteLocationRaw
  /** Opens the connect-client dialog instead of navigating. */
  opens?: 'connect-client'
  requiresRoutePath?: string
}

/** Palette "Actions" section. Shares its targets with the Add menu. */
export const PALETTE_ACTIONS: PaletteAction[] = [
  { id: 'add-server', label: 'Add server', to: '/add-server' },
  { id: 'connect-client', label: 'Connect client', opens: 'connect-client' },
  { id: 'create-token', label: 'Create agent token', to: { path: '/clients', query: { tab: 'tokens', create: '1' } } },
  { id: 'open-review', label: 'Open review queue', to: '/review' },
  { id: 'create-profile', label: 'Create profile', to: { path: '/profiles', query: { create: '1' } }, requiresRoutePath: '/profiles' },
]

/** Server-edition tenant menu (`/my/*`). The palette's Pages source for tenants. */
export const TEAMS_USER_MENU: Array<{ name: string; path: string }> = [
  { name: 'My Servers', path: '/my/servers' },
  { name: 'My Activity', path: '/my/activity' },
  { name: 'Agent Tokens', path: '/my/tokens' },
  { name: 'Diagnostics', path: '/my/diagnostics' },
  // Tools is the canonical search surface since /search folded into it (F20).
  { name: 'Tools', path: '/tools' },
]

/** Server-edition admin menu (`/admin/*`). */
export const TEAMS_ADMIN_MENU: Array<{ name: string; path: string }> = [
  { name: 'Dashboard', path: '/admin/dashboard' },
  { name: 'Server Management', path: '/admin/servers' },
  { name: 'Activity (All)', path: '/activity' },
  { name: 'Users', path: '/admin/users' },
  { name: 'Sessions', path: '/sessions' },
  { name: 'Settings', path: '/settings' },
]
