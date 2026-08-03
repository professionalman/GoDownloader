package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_LoginTableDriven(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		body          string
		setCookie     string
		expectSuccess bool
	}{
		{
			name:          "1. 200 + Ok. + SID",
			statusCode:    http.StatusOK,
			body:          "Ok.",
			setCookie:     "SID=session_id_12345; Path=/; HttpOnly",
			expectSuccess: true,
		},
		{
			name:          "2. 200 + Ok. + QBT_SID_8081",
			statusCode:    http.StatusOK,
			body:          "Ok.",
			setCookie:     "QBT_SID_8081=session_id_67890; Path=/; HttpOnly",
			expectSuccess: true,
		},
		{
			name:          "3. 204 + SID",
			statusCode:    http.StatusNoContent,
			body:          "",
			setCookie:     "SID=session_id_12345; Path=/; HttpOnly",
			expectSuccess: true,
		},
		{
			name:          "4. 204 + QBT_SID_8081",
			statusCode:    http.StatusNoContent,
			body:          "",
			setCookie:     "QBT_SID_8081=session_id_67890; Path=/; HttpOnly",
			expectSuccess: true,
		},
		{
			name:          "5. 204 without cookie",
			statusCode:    http.StatusNoContent,
			body:          "",
			setCookie:     "",
			expectSuccess: false,
		},
		{
			name:          "6. 200 with invalid body",
			statusCode:    http.StatusOK,
			body:          "Fails.",
			setCookie:     "SID=session_id_12345; Path=/; HttpOnly",
			expectSuccess: false,
		},
		{
			name:          "7. invalid credentials",
			statusCode:    http.StatusForbidden,
			body:          "Forbidden",
			setCookie:     "",
			expectSuccess: false,
		},
		{
			name:          "8. unexpected status",
			statusCode:    http.StatusInternalServerError,
			body:          "Server Error",
			setCookie:     "SID=session_id_12345; Path=/; HttpOnly",
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/auth/login" {
					t.Errorf("expected /api/v2/auth/login, got %s", r.URL.Path)
				}
				if tt.setCookie != "" {
					w.Header().Set("Set-Cookie", tt.setCookie)
				}
				w.WriteHeader(tt.statusCode)
				if tt.body != "" {
					_, _ = w.Write([]byte(tt.body))
				}
			}))
			defer ts.Close()

			client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
			err := client.Login(context.Background())

			if tt.expectSuccess {
				if err != nil {
					t.Fatalf("expected login to succeed, got error: %v", err)
				}
				if !client.authenticated {
					t.Errorf("expected client.authenticated to be true")
				}
			} else {
				if err == nil {
					t.Fatalf("expected login to fail, got nil error")
				}
				if client.authenticated {
					t.Errorf("expected client.authenticated to be false")
				}
			}
		})
	}
}

func TestClient_Reauthentication(t *testing.T) {
	t.Run("9. reauthentication after an expired session (401 or 403)", func(t *testing.T) {
		loginCalls := 0
		versionCalls := 0

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				loginCalls++
				w.Header().Set("Set-Cookie", "QBT_SID_8081=new_session_token; Path=/; HttpOnly")
				w.WriteHeader(http.StatusNoContent)
			case "/api/v2/app/version":
				versionCalls++
				if versionCalls == 1 {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("v5.0.0"))
			default:
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
		client.authenticated = true // Simulate previously authenticated state

		ver, err := client.GetVersion(context.Background())
		if err != nil {
			t.Fatalf("expected GetVersion to succeed after reauth, got: %v", err)
		}
		if ver != "v5.0.0" {
			t.Errorf("expected version v5.0.0, got %s", ver)
		}
		if loginCalls != 1 {
			t.Errorf("expected 1 login call, got %d", loginCalls)
		}
		if versionCalls != 2 {
			t.Errorf("expected 2 version calls, got %d", versionCalls)
		}
	})

	t.Run("10. second authentication failure stops retrying", func(t *testing.T) {
		loginCalls := 0
		versionCalls := 0

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v2/auth/login":
				loginCalls++
				w.Header().Set("Set-Cookie", "SID=temp_session; Path=/; HttpOnly")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("Ok."))
			case "/api/v2/app/version":
				versionCalls++
				// Persistent 401
				w.WriteHeader(http.StatusUnauthorized)
			default:
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "admin", "adminadmin", 5*time.Second)
		client.authenticated = true // Simulate previously authenticated state

		_, err := client.GetVersion(context.Background())
		if err == nil {
			t.Fatalf("expected error after second 401, got nil")
		}
		if loginCalls != 1 {
			t.Errorf("expected 1 login attempt before stopping, got %d", loginCalls)
		}
		if versionCalls != 2 {
			t.Errorf("expected 2 version calls max (initial + 1 retry), got %d", versionCalls)
		}
	})
}
