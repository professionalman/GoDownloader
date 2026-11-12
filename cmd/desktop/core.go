package main

import (
	"context"
	"fmt"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
)

// desktopCore owns the application runtime of one desktop process. Background workers (scheduler, progress
// monitor, network reconciler, tracker) run under lifetimeCtx, which ends only when the core shuts down — never
// when initialization returns. Closing the window to the tray does not touch the core; only Shutdown does.
type desktopCore struct {
	app    *app.App
	cancel context.CancelFunc
}

// startDesktopCore constructs the application core and starts its background runtime under a process-lifetime
// context. On any failure after construction it shuts the core down again, so no workers, database handle, or
// runtime ownership lock leak from a failed start.
func startDesktopCore(cfg *config.Config, dbPath string) (*desktopCore, error) {
	appInstance, err := app.New(context.Background(), cfg, app.WithDBPath(dbPath))
	if err != nil {
		return nil, err
	}
	lifetimeCtx, cancel := context.WithCancel(context.Background())
	core := &desktopCore{app: appInstance, cancel: cancel}

	if err := cfg.EnsureDirectories(); err != nil {
		_ = core.Shutdown(5 * time.Second)
		return nil, fmt.Errorf("initialize desktop directories: %w", err)
	}
	if err := appInstance.Start(lifetimeCtx); err != nil {
		_ = core.Shutdown(5 * time.Second)
		return nil, fmt.Errorf("start application runtime: %w", err)
	}
	return core, nil
}

// workersAlive reports whether every long-lived runtime loop is running.
func workersAlive(a *app.App) bool {
	w := a.BackgroundWorkers()
	return w.Scheduler && w.Monitor && w.Reconciler && w.Tracker
}

// waitWorkersAlive polls until all runtime loops report running or within elapses (goroutine start latency).
func waitWorkersAlive(a *app.App, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for !workersAlive(a) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// App returns the running application core.
func (c *desktopCore) App() *app.App {
	return c.app
}

// Shutdown performs the ordered core teardown (tracker, manager loops, process trees, database, ownership lock)
// within timeout, then ends the runtime lifetime context. It is idempotent.
func (c *desktopCore) Shutdown(timeout time.Duration) error {
	ctx, cancelTimeout := context.WithTimeout(context.Background(), timeout)
	defer cancelTimeout()
	err := c.app.Shutdown(ctx)
	c.cancel()
	return err
}
