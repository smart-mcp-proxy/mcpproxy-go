//go:build traydebug && !linux

package tray

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"

	"go.uber.org/zap"
)

type instrumentation interface {
	Start(ctx context.Context)
	NotifyConnectionState(state ConnectionState)
	NotifyStatus()
	NotifyMenus()
	Shutdown()
}

type debugInstrumentation struct {
	app    *App
	logger *zap.SugaredLogger

	once sync.Once
	srv  *http.Server
	ln   net.Listener
}

func newInstrumentation(app *App) instrumentation {
	return &debugInstrumentation{app: app, logger: app.logger}
}

func (d *debugInstrumentation) Start(ctx context.Context) {
	d.once.Do(func() {
		addr := os.Getenv("MCPPROXY_TRAY_INSPECT_ADDR")
		if addr == "" {
			addr = "127.0.0.1:0"
		}

		mux := http.NewServeMux()
		mux.HandleFunc("/health", d.handleHealth)
		mux.HandleFunc("/state", d.handleState)
		mux.HandleFunc("/action", d.handleAction)

		srv := &http.Server{Handler: mux}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("Failed to start tray inspector", "error", err)
			}
			return
		}

		d.srv = srv
		d.ln = ln

		if d.logger != nil {
			d.logger.Infow("Tray inspector listening", "addr", ln.Addr().String())
		}

		go func() {
			<-ctx.Done()
			_ = srv.Shutdown(context.Background())
		}()

		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				if d.logger != nil {
					d.logger.Warn("Tray inspector server error", "error", err)
				}
			}
		}()
	})
}

func (d *debugInstrumentation) NotifyConnectionState(ConnectionState) {}

func (d *debugInstrumentation) NotifyStatus() {}

func (d *debugInstrumentation) NotifyMenus() {}

func (d *debugInstrumentation) Shutdown() {
	if d.srv != nil {
		_ = d.srv.Shutdown(context.Background())
	}
}

func (d *debugInstrumentation) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (d *debugInstrumentation) handleState(w http.ResponseWriter, _ *http.Request) {
	title, tooltip := d.app.getStatusSnapshot()
	servers, quarantine := d.app.getMenuSnapshot()

	resp := map[string]interface{}{
		"connection_state": d.app.getConnectionState(),
		"status":           title,
		"tooltip":          tooltip,
		"servers":          servers,
		"quarantine":       quarantine,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type actionRequest struct {
	Type   string `json:"type"`
	Server string `json:"server"`
	Action string `json:"action"`
}

func (d *debugInstrumentation) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req actionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(err.Error()))
		return
	}

	switch req.Type {
	case "server":
		d.app.handleServerAction(req.Server, req.Action)
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("unsupported action type"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
