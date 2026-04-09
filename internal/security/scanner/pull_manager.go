package scanner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// pullTimeout caps how long a single background docker pull can take.
// Pulls for large scanner images (Trivy ~200MB, AI scanner ~1GB) finish
// well under this, but we bound it anyway so a stuck pull never pins a
// scanner in the "pulling" state forever.
const pullTimeout = 30 * time.Minute

// pullManager runs Docker image pulls for scanners in the background so that
// the caller (HTTP handler / CLI) doesn't block while a ~500MB image is
// downloaded. It also deduplicates concurrent pull requests for the same
// image: two toggles on the same scanner in quick succession queue a single
// pull.
type pullManager struct {
	docker  *DockerRunner
	storage Storage
	emitter func() EventEmitter // late binding — emitter may be wired after NewService
	reg     *Registry
	logger  *zap.Logger

	mu      sync.Mutex
	pending map[string]context.CancelFunc // scannerID → cancel
}

func newPullManager(docker *DockerRunner, storage Storage, reg *Registry, logger *zap.Logger, emitter func() EventEmitter) *pullManager {
	return &pullManager{
		docker:  docker,
		storage: storage,
		reg:     reg,
		logger:  logger,
		emitter: emitter,
		pending: make(map[string]context.CancelFunc),
	}
}

// cancelPending cancels an in-flight pull for the given scanner if any, and
// waits for the goroutine to observe the cancellation. Safe to call even if
// no pull is in flight.
func (p *pullManager) cancelPending(id string) {
	p.mu.Lock()
	cancel, ok := p.pending[id]
	if ok {
		delete(p.pending, id)
	}
	p.mu.Unlock()
	if ok && cancel != nil {
		cancel()
	}
}

// Enqueue starts a background pull for the scanner's effective image. If a
// pull is already in flight it is cancelled and replaced (useful when the
// user changes the image override while a pull is running). Caller should
// have already persisted the scanner with status=ScannerStatusPulling so
// that the first API read after Enqueue reflects the new state.
//
// The caller passes the target status that should be applied on a successful
// pull — typically ScannerStatusConfigured when env vars are set, or
// ScannerStatusInstalled otherwise.
func (p *pullManager) Enqueue(id string, successStatus string) {
	if id == "" {
		return
	}

	p.cancelPending(id)

	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	p.mu.Lock()
	p.pending[id] = cancel
	p.mu.Unlock()

	go p.run(ctx, id, successStatus)
}

// run performs the actual pull in its own goroutine.
func (p *pullManager) run(ctx context.Context, id, successStatus string) {
	defer func() {
		p.mu.Lock()
		if cancel, ok := p.pending[id]; ok {
			cancel()
			delete(p.pending, id)
		}
		p.mu.Unlock()
	}()

	sc, err := p.storage.GetScanner(id)
	if err != nil {
		// Storage row may have been deleted (scanner removed). Nothing to do.
		p.logger.Warn("Background pull: scanner not found in storage", zap.String("id", id), zap.Error(err))
		return
	}

	image := sc.EffectiveImage()
	if image == "" {
		p.markFailed(sc, "scanner has no docker image configured")
		return
	}

	// Fast path: if the image is already present we don't need to pull.
	if p.docker.ImageExists(ctx, image) {
		p.markSuccess(sc, successStatus)
		return
	}

	if !p.docker.IsDockerAvailable(ctx) {
		p.markFailed(sc, "Docker is not available; cannot pull scanner image")
		return
	}

	p.logger.Info("Background scanner image pull started",
		zap.String("scanner", id),
		zap.String("image", image),
	)

	if err := p.docker.PullImage(ctx, image); err != nil {
		if ctx.Err() == context.Canceled {
			p.logger.Info("Background scanner image pull cancelled",
				zap.String("scanner", id), zap.String("image", image))
			return
		}
		p.markFailed(sc, fmt.Sprintf("failed to pull %s: %v", image, err))
		return
	}

	p.logger.Info("Background scanner image pull finished",
		zap.String("scanner", id),
		zap.String("image", image),
	)
	p.markSuccess(sc, successStatus)
}

// markSuccess persists the success state and notifies listeners.
func (p *pullManager) markSuccess(sc *ScannerPlugin, status string) {
	sc.Status = status
	sc.ErrorMsg = ""
	if sc.InstalledAt.IsZero() {
		sc.InstalledAt = time.Now()
	}
	if err := p.storage.SaveScanner(sc); err != nil {
		p.logger.Warn("Failed to persist scanner success state",
			zap.String("scanner", sc.ID), zap.Error(err))
	}
	if p.reg != nil {
		_ = p.reg.UpdateStatus(sc.ID, status)
	}
	if p.emitter != nil {
		if em := p.emitter(); em != nil {
			em.EmitSecurityScannerChanged(sc.ID, status, "")
		}
	}
}

// markFailed persists the error state and notifies listeners.
func (p *pullManager) markFailed(sc *ScannerPlugin, reason string) {
	sc.Status = ScannerStatusError
	sc.ErrorMsg = reason
	if err := p.storage.SaveScanner(sc); err != nil {
		p.logger.Warn("Failed to persist scanner error state",
			zap.String("scanner", sc.ID), zap.Error(err))
	}
	if p.reg != nil {
		_ = p.reg.UpdateStatus(sc.ID, ScannerStatusError)
	}
	if p.emitter != nil {
		if em := p.emitter(); em != nil {
			em.EmitSecurityScannerChanged(sc.ID, ScannerStatusError, reason)
		}
	}
	p.logger.Warn("Scanner image pull failed",
		zap.String("scanner", sc.ID),
		zap.String("reason", reason),
	)
}
