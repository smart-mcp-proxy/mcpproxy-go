# Frontend

## Stack

- Vue 3 with Composition API
- TypeScript
- Vite build tool

## Directory Structure

```
frontend/
├── src/
│   ├── components/     # Vue components
│   ├── views/          # Page views
│   ├── api/            # API client
│   └── types/          # TypeScript types
├── public/             # Static assets
└── vite.config.ts      # Vite configuration
```

## Development

```bash
cd frontend
npm install
npm run dev           # Development server
npm run build         # Production build
npm run lint          # Lint check
```

## API Integration

Frontend connects to core via:
- REST API (`/api/v1/*`)
- SSE for real-time updates (`/events`)

API key passed via `?apikey=` query parameter.

## Building with Backend

```bash
make build            # Builds both frontend and backend
```

Frontend assets embedded in Go binary for single-file distribution.

## Real-time Updates

SSE connection to `/events` endpoint:
- `servers.changed` - Refresh server list
- `config.reloaded` - Refresh configuration

## Web UI Access

```bash
open "http://127.0.0.1:8080/ui/?apikey=your-api-key"
```

Tray app opens Web UI automatically with correct API key.
