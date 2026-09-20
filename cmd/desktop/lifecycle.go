package main

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"downloader/internal/app"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// DesktopLifecycle coordinates desktop window close-to-tray and application shutdown.
type DesktopLifecycle struct {
	quitting       atomic.Bool
	trayConfigured atomic.Bool
	closeToTray    atomic.Bool
	trayShowCount  atomic.Int32
	trayQuitCount  atomic.Int32
	mu             sync.Mutex
	app            *app.App
	wailsApp       *application.App
	mainWindow     *application.WebviewWindow
	shutdownCount  int32
}

// NewDesktopLifecycle creates a new lifecycle coordinator.
func NewDesktopLifecycle() *DesktopLifecycle {
	l := &DesktopLifecycle{}
	l.closeToTray.Store(true)
	return l
}

// SetWailsContext registers the application, main window, and backend runtime references.
func (l *DesktopLifecycle) SetWailsContext(wailsApp *application.App, win *application.WebviewWindow, appInstance *app.App) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.wailsApp = wailsApp
	l.mainWindow = win
	l.app = appInstance
}

// SetTrayConfigured records whether system tray configuration succeeded.
// NOTE (Wails v3.0.0-beta.23 Semantics): SystemTrayManager.New() creates in-memory descriptors
// and defers Shell_NotifyIcon(NIM_ADD) execution to App.Run(). Wails catches Shell_NotifyIcon failures
// internally as warnings and automatically handles re-registration via TaskbarCreated window messages.
// Therefore, trayConfigured represents host configuration readiness, while secondary single-instance
// launch provides the unconditional safety fallback to unhide and restore the window.
func (l *DesktopLifecycle) SetTrayConfigured(configured bool) {
	l.trayConfigured.Store(configured)
}

// IsTrayConfigured returns whether the system tray has been configured.
func (l *DesktopLifecycle) IsTrayConfigured() bool {
	return l.trayConfigured.Load()
}

// IsQuitting returns whether application exit has been requested.
func (l *DesktopLifecycle) IsQuitting() bool {
	return l.quitting.Load()
}

// RecordTrayShow increments the diagnostic counter for tray Show activations.
func (l *DesktopLifecycle) RecordTrayShow() {
	l.trayShowCount.Add(1)
}

// TrayShowCount returns the count of tray Show activations.
func (l *DesktopLifecycle) TrayShowCount() int {
	return int(l.trayShowCount.Load())
}

// RecordTrayQuit increments the diagnostic counter for tray Quit activations.
func (l *DesktopLifecycle) RecordTrayQuit() {
	l.trayQuitCount.Add(1)
}

// TrayQuitCount returns the count of tray Quit activations.
func (l *DesktopLifecycle) TrayQuitCount() int {
	return int(l.trayQuitCount.Load())
}

// SetCloseToTray configures whether closing the window hides it to tray instead of quitting.
func (l *DesktopLifecycle) SetCloseToTray(enabled bool) {
	l.closeToTray.Store(enabled)
}

// CloseToTray returns whether closing the window hides it to tray.
func (l *DesktopLifecycle) CloseToTray() bool {
	return l.closeToTray.Load()
}

// ShouldHideOnClose determines whether the window should hide to tray instead of closing.
// Pure decision logic:
// - If quitting: do not hide (allow real window close and process termination).
// - If tray is configured and closeToTray is enabled: hide (close-to-tray).
// - If tray is not configured or closeToTray is disabled: do not hide (allow normal close to prevent unreachable headless process).
func (l *DesktopLifecycle) ShouldHideOnClose() bool {
	return !l.quitting.Load() && l.trayConfigured.Load() && l.closeToTray.Load()
}

// HandleWindowClosing handles the Wails WindowClosing event.
func (l *DesktopLifecycle) HandleWindowClosing(e *application.WindowEvent, win *application.WebviewWindow) {
	if l.ShouldHideOnClose() {
		if e != nil {
			e.Cancel()
		}
		if win != nil {
			win.Hide()
		}
		return
	}
	// Otherwise allow close to proceed normally
}

// ShowMainWindow restores and focuses the main application window.
func (l *DesktopLifecycle) ShowMainWindow() {
	l.mu.Lock()
	win := l.mainWindow
	l.mu.Unlock()

	showMainWindow(win)
}

// showMainWindow is a standalone helper that restores and brings a window to front.
func showMainWindow(win *application.WebviewWindow) {
	if win != nil {
		win.Show()
		win.Restore()
		win.Focus()
	}
}

// RequestQuit initiates orderly application shutdown exactly once.
func (l *DesktopLifecycle) RequestQuit() {
	if l.quitting.Swap(true) {
		// Already quitting; avoid duplicate shutdown sequence
		return
	}
	atomic.AddInt32(&l.shutdownCount, 1)
	log.Println("Desktop: Orderly quit initiated.")

	l.mu.Lock()
	appInst := l.app
	wailsApp := l.wailsApp
	l.mu.Unlock()

	if appInst != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := appInst.Shutdown(ctx); err != nil {
			log.Printf("Desktop: App.Shutdown error: %v", err)
		}
	}
	if wailsApp != nil {
		wailsApp.Quit()
	}
}
