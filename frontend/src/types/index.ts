export * from './api'
// Selectively export types from contracts.ts that don't conflict with api.ts
export type {
  UpdateInfo,
  InfoResponse,
  OAuthConfig,
  IsolationConfig,
  ServerStats,
  LogEntry,
  SystemStatus,
  RuntimeStatus,
  ToolCallRequest,
  ToolCallResponse,
  Event,
  Ref,
  GetServersResponse,
  GetServerToolsResponse,
  SearchToolsResponse,
  GetServerLogsResponse,
  ServerActionResponse,
  QuarantinedServersResponse,
  ServerAction,
  ServerToggleRequest,
  SearchRequest,
  LogsRequest,
  SSEEventType,
  SSEEvent,
  APIError,
  APISuccess,
  APIResult,
} from './contracts'
export { isAPIError, isAPISuccess } from './contracts'

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