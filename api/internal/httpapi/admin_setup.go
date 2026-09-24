package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/skip2/go-qrcode"
	"golang.org/x/crypto/bcrypt"
	"minitube/api/internal/totp"
)

const setupLANCIDR = "192.168.43.0/24"
const setupLANHost = "192.168.43.111"

var setupLANNet = func() *net.IPNet {
	_, n, err := net.ParseCIDR(setupLANCIDR)
	if err != nil {
		panic(err)
	}
	return n
}()

func (s *Server) requireSetupIntranet(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if setupRequestForbidden(r, s.publicSetupHosts()) {
			if strings.HasPrefix(r.URL.Path, "/v1/") {
				writeError(w, http.StatusNotFound, "not_found", "not found")
				return
			}
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) publicSetupHosts() map[string]bool {
	out := map[string]bool{
		"minitube.19121122.xyz":     true,
		"ctc.minitube.19121122.xyz": true,
	}
	if raw := strings.TrimSpace(s.cfg.PublicBaseURL); raw != "" {
		if u, err := url.Parse(raw); err == nil {
			if host := strings.ToLower(strings.TrimSpace(u.Hostname())); host != "" {
				out[host] = true
			}
		}
	}
	return out
}

func setupRequestForbidden(r *http.Request, publicHosts map[string]bool) bool {
	if strings.TrimSpace(r.Header.Get("CF-Connecting-IP")) != "" {
		return true
	}
	host := strings.ToLower(requestHost(r))
	if publicHosts[host] {
		return true
	}
	ip := net.ParseIP(setupPeerIP(r))
	if ip == nil {
		return true
	}
	if inSetupLAN(ip.String()) {
		return false
	}
	if ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
		return true
	}
	return !isSetupLANHost(host)
}

func isSetupLANHost(host string) bool {
	return host == setupLANHost
}

func requestHost(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" && r.URL != nil {
		host = strings.TrimSpace(r.URL.Host)
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func setupPeerIP(r *http.Request) string {
	if ip := parseClientIP(r.Header.Get("X-Real-IP")); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	if ip := net.ParseIP(strings.TrimSpace(r.RemoteAddr)); ip != nil {
		return ip.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func inSetupLAN(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	return setupLANNet.Contains(ip)
}

func (s *Server) adminSetupStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminEnabled() {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totpEnrolled": s.totpEnrolled(),
	})
}

func (s *Server) adminSetupLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if retryAfter, locked := s.adminLoginBlocked(ip); locked {
		s.writeAdminLocked(w, retryAfter)
		return
	}
	s.consumeAdminLoginAttempt(ip)
	acct, ok := s.snapshotAccount()
	if !ok || strings.TrimSpace(acct.passwordHash) == "" {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
		return
	}
	var req struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		s.recordAdminLoginFailure(ip)
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if !adminHashOK(req.Password, acct.passwordHash) {
		s.recordAdminLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "bad_password", "bad password")
		return
	}
	if acct.totpEnrolled && strings.TrimSpace(acct.totpSecret) != "" {
		if !totp.Validate(acct.totpSecret, req.TOTP, s.now()) {
			s.recordAdminLoginFailure(ip)
			writeError(w, http.StatusUnauthorized, "bad_totp", "bad totp")
			return
		}
	}
	s.clearAdminLoginFailure(ip)
	token, err := s.issueNamedToken(&s.setupTok)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "token error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        token,
		"totpEnrolled": acct.totpEnrolled && strings.TrimSpace(acct.totpSecret) != "",
	})
}

func (s *Server) requireSetupAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminEnabled() {
			writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
			return
		}
		token := bearerToken(r)
		if token == "" || !s.namedTokenOK("setup", token) {
			writeError(w, http.StatusUnauthorized, "need_auth", "need auth")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminSetupPassword(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.snapshotAccount()
	if !ok {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if !adminHashOK(req.CurrentPassword, acct.passwordHash) {
		writeError(w, http.StatusUnauthorized, "bad_password", "bad password")
		return
	}
	newPass := strings.TrimSpace(req.NewPassword)
	if newPass == "" {
		writeError(w, http.StatusBadRequest, "empty_password", "empty password")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_error", "hash error")
		return
	}
	acct.passwordHash = string(hash)
	if err := s.persistAccount(r.Context(), acct); err != nil {
		slog.Error("admin setup password persist failed")
		writeErr(w, err)
		return
	}
	s.clearPublicAdminSessions()
	slog.Info("admin password updated")
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) adminSetupTOTPBegin(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.snapshotAccount()
	if !ok {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
		return
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp_error", "totp error")
		return
	}
	acct.totpPending = secret
	acct.totpSecret = ""
	acct.totpEnrolled = false
	if err := s.persistAccount(r.Context(), acct); err != nil {
		slog.Error("admin setup totp begin persist failed")
		writeErr(w, err)
		return
	}
	s.clearPublicAdminSessions()
	otpauth := totp.OTPAuthURL(secret)
	png, err := qrcode.Encode(otpauth, qrcode.Medium, 256)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "totp_error", "qr error")
		return
	}
	slog.Info("admin totp enroll pending")
	writeJSON(w, http.StatusOK, map[string]any{
		"secret":       secret,
		"otpauthUrl":   otpauth,
		"qrPng":        "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		"totpEnrolled": false,
	})
}

func (s *Server) adminSetupTOTPConfirm(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.snapshotAccount()
	if !ok {
		writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	pending := strings.TrimSpace(acct.totpPending)
	if pending == "" {
		writeError(w, http.StatusBadRequest, "no_pending", "no pending totp")
		return
	}
	if !totp.Validate(pending, req.Code, s.now()) {
		writeError(w, http.StatusUnauthorized, "bad_totp", "bad totp")
		return
	}
	acct.totpSecret = pending
	acct.totpPending = ""
	acct.totpEnrolled = true
	if err := s.persistAccount(r.Context(), acct); err != nil {
		slog.Error("admin setup totp confirm persist failed")
		writeErr(w, err)
		return
	}
	s.clearPublicAdminSessions()
	slog.Info("admin totp enrolled")
	writeJSON(w, http.StatusOK, map[string]any{
		"totpEnrolled": true,
		"status":       "enrolled",
	})
}
