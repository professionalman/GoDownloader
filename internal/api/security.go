package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"downloader/internal/config"
)

const (
	SessionCookieName = "godownloader_session"
	CSRFCookieName    = "godownloader_csrf"
	CSRFHeaderName    = "X-CSRF-Token"
)

var (
	ErrNonLoopbackForbidden = errors.New("binding to non-loopback address is forbidden")
)

// SecurityManager coordinates local authenticated surfaces and browser trust boundaries.
type SecurityManager struct {
	sessionToken   string
	csrfToken      string
	allowedOrigins map[string]bool
	allowedHosts   map[string]bool
	listenPort     string
	devMode        bool
	disabled       bool
}

// NewSecurityManager initializes an ephemeral in-memory session and trust configuration.
func NewSecurityManager(cfg *config.Config) *SecurityManager {
	sessionToken := generateSecureRandomToken(32)
	csrfToken := generateSecureRandomToken(32)

	disabled := false
	devMode := false
	listenPort := "8080"
	listenHost := "127.0.0.1"

	if cfg != nil {
		disabled = cfg.DisableAuth
		devMode = cfg.DevMode
		if cfg.ListenAddr != "" {
			if h, p, err := net.SplitHostPort(cfg.ListenAddr); err == nil {
				if h != "" {
					listenHost = h
				}
				if p != "" {
					listenPort = p
				}
			}
		}
	}

	allowedHosts := map[string]bool{
		"localhost": true,
		"127.0.0.1": true,
		"::1":       true,
		"[::1]":     true,
		listenHost:  true,
	}

	allowedOrigins := map[string]bool{
		"http://localhost:" + listenPort: true,
		"http://127.0.0.1:" + listenPort: true,
		"http://[::1]:" + listenPort:     true,
	}

	// Development origin is strictly explicit and opt-in via GODOWNLOADER_DEV_MODE=1
	if devMode {
		allowedOrigins["http://localhost:5173"] = true
		allowedOrigins["http://127.0.0.1:5173"] = true
		allowedOrigins["http://[::1]:5173"] = true
	}

	return &SecurityManager{
		sessionToken:   sessionToken,
		csrfToken:      csrfToken,
		allowedOrigins: allowedOrigins,
		allowedHosts:   allowedHosts,
		listenPort:     listenPort,
		devMode:        devMode,
		disabled:       disabled,
	}
}

// Generate a cryptographically secure random hex string.
func generateSecureRandomToken(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		// Fallback should never happen on modern OS
		panic(fmt.Sprintf("failed to read secure random bytes: %v", err))
	}
	return hex.EncodeToString(b)
}

// SessionToken returns the active in-memory session token (for internal/test verification).
func (sm *SecurityManager) SessionToken() string {
	return sm.sessionToken
}

// CSRFToken returns the active in-memory CSRF token.
func (sm *SecurityManager) CSRFToken() string {
	return sm.csrfToken
}

// IsDisabled reports whether authentication checks are bypassed (e.g. testing).
func (sm *SecurityManager) IsDisabled() bool {
	return sm.disabled
}

// CheckNonLoopbackBind validates that the configured listen address is strictly loopback-only.
// Binding to non-loopback addresses (0.0.0.0, LAN IPs, etc.) is unconditionally rejected.
func CheckNonLoopbackBind(listenAddr string) error {
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		host = listenAddr
	}

	// Empty host means binding to all interfaces (e.g. ":8080") -> non-loopback
	isLoopback := false
	if host != "" {
		if host == "localhost" {
			isLoopback = true
		} else if ip := net.ParseIP(host); ip != nil {
			isLoopback = ip.IsLoopback()
		}
	}

	if !isLoopback {
		return fmt.Errorf("%w: %s", ErrNonLoopbackForbidden, listenAddr)
	}
	return nil
}

// ValidateHost validates that the request Host header matches an allowed local loopback host,
// preventing DNS rebinding attacks. Host validation is a browser DNS-rebinding defense only;
// it does NOT authenticate raw HTTP clients.
func (sm *SecurityManager) ValidateHost(r *http.Request) bool {
	if sm.disabled {
		return true
	}

	host := r.Host
	if host == "" {
		return false
	}

	// Strip port if present
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}

	// Clean IPv6 brackets if any
	h = strings.Trim(h, "[]")

	if sm.allowedHosts[h] || sm.allowedHosts["["+h+"]"] {
		return true
	}

	// Allow loopback IPs (127.0.0.0/8)
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}

	return false
}

// ValidateOrigin validates that if an Origin header is present, it belongs to an allowed local origin.
func (sm *SecurityManager) ValidateOrigin(r *http.Request) bool {
	if sm.disabled {
		return true
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// If Origin is omitted, check Referer as a fallback for browser mutating requests
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			if ref := r.Header.Get("Referer"); ref != "" {
				if parsed, err := url.Parse(ref); err == nil {
					refOrigin := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
					return sm.isAllowedOriginString(refOrigin)
				}
			}
		}
		// Non-browser or same-origin GET without Origin header is allowed
		return true
	}

	return sm.isAllowedOriginString(origin)
}

func (sm *SecurityManager) isAllowedOriginString(origin string) bool {
	if sm.allowedOrigins[origin] {
		return true
	}

	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	host := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "http" {
			port = "80"
		} else {
			port = "443"
		}
	}

	// Host must be loopback
	isLoopbackHost := (host == "localhost" || host == "127.0.0.1" || host == "::1")
	if !isLoopbackHost {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			isLoopbackHost = true
		}
	}

	if isLoopbackHost {
		// Only the configured listenPort is accepted in production
		if port == sm.listenPort {
			return true
		}
		// In development mode, Vite dev port 5173 is accepted
		if sm.devMode && port == "5173" {
			return true
		}
	}

	return false
}

// ValidateSession validates the session credential strictly via HttpOnly cookie.
// Query parameter tokens and Bearer headers are not accepted for browser session auth.
func (sm *SecurityManager) ValidateSession(r *http.Request) bool {
	if sm.disabled {
		return true
	}

	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(sm.sessionToken)) == 1
}

// ValidateCSRF validates anti-CSRF protection for state-mutating requests (POST, PUT, DELETE).
func (sm *SecurityManager) ValidateCSRF(r *http.Request) bool {
	if sm.disabled {
		return true
	}

	// Safe methods (GET, HEAD, OPTIONS) do not require CSRF token
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}

	// Verify X-CSRF-Token header against in-memory CSRF token
	csrfHeader := r.Header.Get(CSRFHeaderName)
	if csrfHeader == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(csrfHeader), []byte(sm.csrfToken)) == 1
}

// SetSessionCookies writes the SameSite=Strict session and CSRF cookies to the HTTP response.
func (sm *SecurityManager) SetSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    sm.sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    sm.csrfToken,
		Path:     "/",
		HttpOnly: false, // Readable by client JavaScript to supply in X-CSRF-Token header
		SameSite: http.SameSiteStrictMode,
	})
}

// SessionBootstrapHandler serves GET /api/v1/auth/session to initialize the local browser session.
// Sets HttpOnly session cookie and CSRF cookie.
// Returns JSON with status and csrfToken only; the sessionToken is strictly confined to HttpOnly cookie.
func (sm *SecurityManager) SessionBootstrapHandler(w http.ResponseWriter, r *http.Request) {
	if !sm.ValidateHost(r) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "invalid host header")
		return
	}

	if !sm.ValidateOrigin(r) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "cross-origin request forbidden")
		return
	}

	sm.SetSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":    "ok",
		"csrfToken": sm.csrfToken,
	})
}

// SecurityMiddleware creates an http.Handler middleware enforcing Host, Origin, Session, and CSRF policies.
func (sm *SecurityManager) SecurityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Host Validation (DNS Rebinding protection)
		if !sm.ValidateHost(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "invalid host header")
			return
		}

		// Public endpoint: Session bootstrap
		if r.URL.Path == "/api/v1/auth/session" {
			next.ServeHTTP(w, r)
			return
		}

		// 2. Origin Validation (prevent unauthorized cross-origin access)
		if !sm.ValidateOrigin(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "cross-origin request forbidden")
			return
		}

		// 3. Session Validation for protected routes
		if !sm.ValidateSession(r) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
			return
		}

		// 4. CSRF Validation for mutating routes
		if !sm.ValidateCSRF(r) {
			writeError(w, http.StatusForbidden, "FORBIDDEN", "CSRF validation failed")
			return
		}

		next.ServeHTTP(w, r)
	})
}
