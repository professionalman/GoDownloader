package main

import (
	"sync"
	"testing"
)

func TestLifecyclePolicy_ShouldHideOnClose(t *testing.T) {
	tests := []struct {
		name           string
		trayConfigured bool
		closeToTray    bool
		quitting       bool
		expected       bool
	}{
		{
			name:           "tray configured, closeToTray true, and not quitting -> hide",
			trayConfigured: true,
			closeToTray:    true,
			quitting:       false,
			expected:       true,
		},
		{
			name:           "tray configured, closeToTray false, and not quitting -> allow close",
			trayConfigured: true,
			closeToTray:    false,
			quitting:       false,
			expected:       false,
		},
		{
			name:           "tray not configured, closeToTray true, and not quitting -> allow close (safe fallback)",
			trayConfigured: false,
			closeToTray:    true,
			quitting:       false,
			expected:       false,
		},
		{
			name:           "tray configured, closeToTray true, but quitting -> allow close",
			trayConfigured: true,
			closeToTray:    true,
			quitting:       true,
			expected:       false,
		},
		{
			name:           "tray not configured and quitting -> allow close",
			trayConfigured: false,
			closeToTray:    true,
			quitting:       true,
			expected:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewDesktopLifecycle()
			l.SetTrayConfigured(tc.trayConfigured)
			l.SetCloseToTray(tc.closeToTray)
			if tc.quitting {
				l.quitting.Store(true)
			}
			actual := l.ShouldHideOnClose()
			if actual != tc.expected {
				t.Errorf("ShouldHideOnClose() = %v, expected %v", actual, tc.expected)
			}
		})
	}
}

func TestLifecycle_CloseToTray_LiveUpdate(t *testing.T) {
	l := NewDesktopLifecycle()
	l.SetTrayConfigured(true)

	// 1. Defaults to true
	if !l.CloseToTray() {
		t.Fatalf("expected CloseToTray() to default to true")
	}
	if !l.ShouldHideOnClose() {
		t.Fatalf("expected ShouldHideOnClose() to be true by default with tray configured")
	}

	// 2. Live update to false
	l.SetCloseToTray(false)
	if l.CloseToTray() {
		t.Fatalf("expected CloseToTray() to be false after SetCloseToTray(false)")
	}
	if l.ShouldHideOnClose() {
		t.Fatalf("expected ShouldHideOnClose() to be false when closeToTray is disabled")
	}

	// 3. Live update back to true
	l.SetCloseToTray(true)
	if !l.CloseToTray() {
		t.Fatalf("expected CloseToTray() to be true after SetCloseToTray(true)")
	}
	if !l.ShouldHideOnClose() {
		t.Fatalf("expected ShouldHideOnClose() to be true when closeToTray is re-enabled")
	}
}

func TestLifecycle_RequestQuit_Idempotent(t *testing.T) {
	l := NewDesktopLifecycle()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.RequestQuit()
		}()
	}
	wg.Wait()

	if !l.IsQuitting() {
		t.Errorf("expected IsQuitting() to be true")
	}
	if l.shutdownCount != 1 {
		t.Errorf("expected shutdownCount == 1, got %d", l.shutdownCount)
	}
}

func TestLifecycle_TrayCounters(t *testing.T) {
	l := NewDesktopLifecycle()
	if l.TrayShowCount() != 0 {
		t.Errorf("expected initial TrayShowCount=0, got %d", l.TrayShowCount())
	}
	if l.TrayQuitCount() != 0 {
		t.Errorf("expected initial TrayQuitCount=0, got %d", l.TrayQuitCount())
	}

	l.RecordTrayShow()
	l.RecordTrayShow()
	if l.TrayShowCount() != 2 {
		t.Errorf("expected TrayShowCount=2, got %d", l.TrayShowCount())
	}

	l.RecordTrayQuit()
	if l.TrayQuitCount() != 1 {
		t.Errorf("expected TrayQuitCount=1, got %d", l.TrayQuitCount())
	}
}

func TestLifecycle_HandleWindowClosing_NilSafe(t *testing.T) {
	l := NewDesktopLifecycle()
	l.SetTrayConfigured(true)

	// Verify HandleWindowClosing doesn't panic when WindowEvent or WebviewWindow is nil
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("HandleWindowClosing panicked: %v", r)
		}
	}()

	l.HandleWindowClosing(nil, nil)

	l.SetTrayConfigured(false)
	l.HandleWindowClosing(nil, nil)
}
