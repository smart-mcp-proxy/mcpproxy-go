# MCPProxy Frontend

A modern Vue 3 + TypeScript control panel for MCPProxy with DaisyUI styling and real-time updates.

## 🚀 Features

- **Modern Stack**: Vue 3 with Composition API, TypeScript, and Vite
- **Beautiful UI**: DaisyUI components with TailwindCSS styling
- **Real-time Updates**: Server-Sent Events (SSE) for live status updates
- **Responsive Design**: Mobile-friendly interface
- **Type Safety**: Full TypeScript support with comprehensive type definitions
- **Testing**: Vitest with Vue Test Utils for component and unit testing
- **Development**: Hot reload with Vite development server

## 📁 Project Structure

```
frontend/
├── public/                 # Static assets
├── src/
│   ├── components/        # Reusable Vue components
│   │   ├── NavBar.vue    # Navigation bar
│   │   ├── ServerCard.vue # Server status card
│   │   └── ToastContainer.vue # Toast notifications
│   ├── services/         # API service layer
│   │   └── api.ts       # HTTP client for backend communication
│   ├── stores/          # Pinia state management
│   │   ├── servers.ts   # Server management state
│   │   ├── system.ts    # System-wide state and notifications
│   │   └── tools.ts     # Tool search and management
│   ├── types/           # TypeScript type definitions
│   │   ├── api.ts       # API response types
│   │   └── index.ts     # Shared types
│   ├── views/           # Page components
│   │   ├── Dashboard.vue # Main dashboard
│   │   ├── Servers.vue  # Server management
│   │   ├── Tools.vue    # Tool discovery
│   │   ├── Search.vue   # Tool search
│   │   └── Settings.vue # Configuration
│   ├── App.vue          # Root component
│   ├── main.ts          # Application entry point
│   └── router.ts        # Vue Router configuration
├── package.json         # Dependencies and scripts
├── vite.config.ts      # Vite build configuration
├── vitest.config.ts    # Testing configuration
├── eslint.config.js    # ESLint configuration
├── tailwind.config.cjs # TailwindCSS + DaisyUI config
└── README.md           # This file
```

## 🛠️ Development Setup

### Prerequisites

- Node.js 20+
- npm or pnpm

### Installation

```bash
# Install dependencies
npm install

# Start development server
npm run dev
```

The development server will start at `http://localhost:3000` with hot reload enabled.

## 📜 Available Scripts

```bash
# Development
npm run dev              # Start Vite dev server with hot reload
npm run build           # Build for production
npm run preview         # Preview production build locally

# Code Quality
npm run type-check      # TypeScript type checking
npm run lint            # ESLint with auto-fix
npm run lint --fix      # Fix ESLint issues automatically

# Testing
npm run test            # Run tests with Vitest
npm run test:ui         # Run tests with Vitest UI
npm run coverage        # Generate test coverage report
```

## 🔧 Build Integration

The frontend integrates with the Go backend build system via the root Makefile:

```bash
# Development workflow
make backend-dev        # Build backend in development mode
make frontend-dev       # Start frontend dev server

# Production build
make build             # Build both frontend and backend
make frontend-build    # Build frontend only
```

### Build Modes

1. **Development Mode** (`make backend-dev`):
   - Go backend serves files from `frontend/dist/`
   - Frontend runs on `:3000` with hot reload
   - API requests proxy to backend on `:8080`

2. **Production Mode** (`make build`):
   - Frontend built and embedded into Go binary
   - Single binary serves both API and UI
   - Accessed via `/ui/` route

## 🎨 UI Components

### ServerCard
Displays server status with actions:
- **Status indicators**: Connected, disconnected, quarantined
- **Protocol badges**: HTTP, stdio, streamable-http
- **Action buttons**: Enable/disable, restart, OAuth login
- **Tool count**: Number of available tools

### ToastContainer
Global notification system:
- **Success notifications**: Green with checkmark
- **Error notifications**: Red with X icon
- **Info notifications**: Blue with info icon
- **Auto-dismiss**: Configurable timeout

### NavBar
Application navigation:
- **Active route highlighting**
- **Responsive mobile menu**
- **Tool search integration**

## 🗄️ State Management

### Pinia Stores

**`servers.ts`** - Server Management:
```typescript
const serversStore = useServersStore()
await serversStore.fetchServers()
serversStore.enableServer('server-name')
```

**`tools.ts`** - Tool Discovery:
```typescript
const toolsStore = useToolsStore()
await toolsStore.searchTools('create issue')
```

**`system.ts`** - Global State:
```typescript
const systemStore = useSystemStore()
systemStore.showToast('Success!', 'success')
```

## 🔌 API Integration

### Service Layer
The `api.ts` service provides typed methods for backend communication:

```typescript
import apiService from '@/services/api'

// Get all servers
const response = await apiService.getServers()
if (response.success) {
  console.log(response.data.servers)
}

// Search tools
const results = await apiService.searchTools('github', 5)
```

### Real-time Updates
Server-Sent Events provide live updates:

```typescript
// Auto-reconnecting SSE client
const eventSource = apiService.subscribeToEvents((event) => {
  if (event.type === 'server_status') {
    serversStore.updateServerStatus(event.data)
  }
})
```

## 🧪 Testing

### Component Testing
```typescript
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'

test('displays server info', () => {
  const wrapper = mount(ServerCard, {
    props: { server: mockServer },
    global: { plugins: [createPinia()] }
  })
  expect(wrapper.text()).toContain('Connected')
})
```

### Service Testing
```typescript
import { vi } from 'vitest'
import apiService from '@/services/api'

test('makes API request', async () => {
  global.fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: () => ({ success: true, data: [] })
  })

  const result = await apiService.getServers()
  expect(result.success).toBe(true)
})
```

## 🎯 Type Safety

### API Response Types
```typescript
interface APIResponse<T> {
  success: boolean
  data?: T
  error?: string
}

interface Server {
  name: string
  protocol: 'http' | 'stdio' | 'streamable-http'
  enabled: boolean
  connected: boolean
  tool_count: number
  url?: string
}
```

### Component Props
```typescript
interface ServerCardProps {
  server: Server
  showActions?: boolean
}
```

## 🚀 Production Deployment

The frontend is embedded into the Go binary during production builds:

1. **Frontend Build**: `npm run build` creates optimized bundles in `dist/`
2. **Copy Assets**: Build process copies `dist/` to `web/frontend/dist/`
3. **Go Embed**: `//go:embed all:frontend/dist` includes files in binary
4. **Serve**: Backend serves frontend from `/ui/` route

### Environment Variables

**Development**:
- `VITE_API_BASE_URL`: Backend API URL (default: `http://localhost:8080`)

**Production**:
- API calls use relative URLs (same origin as frontend)

## 🔧 Configuration Files

### `vite.config.ts`
- Vue plugin setup
- Development proxy configuration
- Build optimization settings

### `tailwind.config.cjs`
- DaisyUI theme configuration
- Custom color schemes
- Component customizations

### `vitest.config.ts`
- Test environment setup (jsdom)
- Coverage configuration
- Path aliases

## 📚 Key Dependencies

### Core Framework
- **Vue 3**: Composition API, reactivity, components
- **TypeScript**: Type safety and developer experience
- **Vite**: Fast dev server and optimized builds

### UI & Styling
- **DaisyUI**: Pre-built component library
- **TailwindCSS**: Utility-first CSS framework
- **Heroicons**: SVG icon library

### State & Routing
- **Pinia**: Vue store with TypeScript support
- **Vue Router**: Client-side routing

### Testing & Quality
- **Vitest**: Fast unit testing framework
- **Vue Test Utils**: Vue component testing utilities
- **ESLint**: Code linting and formatting

## 🤝 Contributing

1. **Component Development**: Create reusable components in `src/components/`
2. **Type Definitions**: Add new types to `src/types/`
3. **API Integration**: Extend `src/services/api.ts` for new endpoints
4. **Testing**: Add tests alongside components in `__tests__/` directories
5. **Documentation**: Update this README for new features

### Code Style

- Use Composition API over Options API
- Prefer `<script setup>` syntax
- Add TypeScript types for all props and emits
- Follow Vue 3 style guide conventions
- Write tests for new components and services

### Pull Request Checklist

- [ ] Tests pass (`npm run test`)
- [ ] TypeScript compiles (`npm run type-check`)
- [ ] Linting passes (`npm run lint`)
- [ ] Build succeeds (`npm run build`)
- [ ] Components are responsive
- [ ] Accessibility considered (ARIA labels, keyboard navigation)

---

For backend integration details, see the main project [README.md](../README.md) and [CLAUDE.md](../CLAUDE.md).