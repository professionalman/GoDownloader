package main

import (
	"sync"
	"testing"
)

func TestLifecyclePolicy_ShouldHideOnClose(t *testing.T) {
	tests := []struct {
		name           string
		trayConfigured bool
		quitting       bool
		expected       bool
	}{
		{
			name:           "tray configured and not quitting -> hide",
			trayConfigured: true,
			quitting:       false,
			expected:       true,
		},
		{
			name:           "tray not configured and not quitting -> allow close (safe fallback)",
			trayConfigured: false,
			quitting:       false,
			expected:       false,
		},
		{
			name:           "tray configured but quitting -> allow close",
			trayConfigured: true,
			quitting:       true,
			expected:       false,
		},
		{
			name:           "tray not configured and quitting -> allow close",
			trayConfigured: false,
			quitting:       true,
			expected:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l := NewDesktopLifecycle()
			l.SetTrayConfigured(tc.trayConfigured)
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
