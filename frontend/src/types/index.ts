export * from './api'

// UI types
export interface Theme {
  name: string
  displayName: string
  dark: boolean
}

export interface MenuItem {
  name: string
  path: string
  icon?: string
  external?: boolean
}

export interface Toast {
  id: string
  type: 'success' | 'error' | 'warning' | 'info'
  title: string
  message?: string
  duration?: number
}

// Component prop types
export interface LoadingState {
  loading: boolean
  error?: string | null
}

export interface PaginationState {
  page: number
  limit: number
  total: number
}