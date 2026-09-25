package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"minitube/api/internal/store"
	"minitube/api/internal/totp"
)

type adminAccount struct {
	passwordHash string
	totpSecret   string
	totpPending  string
	totpEnrolled bool
}

const adminTokenTTL = 2 * time.Hour

const (
	adminLoginFailLockAfter = 3
	adminLoginLockMax       = 15 * time.Minute
	adminFailPruneAfter     = 30 * time.Minute
	adminLoginMaxAttempts   = 8
	adminLoginAttemptWindow = time.Minute
)

type adminLoginState struct {
	failures    int
	lockedUntil time.Time
	lastSeen    time.Time
	attempts    []time.Time
}

func (s *Server) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s *Server) adminLoginLockStatus(w http.ResponseWriter, r *http.Request) {
	retryAfter, locked := s.adminLoginBlocked(clientIP(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"locked":       locked,
		"retryAfter":   retryAfter,
		"totpEnrolled": s.totpEnrolled(),
		"adminEnabled": s.adminEnabled(),
	})
}

func (s *Server) writeAdminLocked(w http.ResponseWriter, retryAfter int) {
	writeJSON(w, http.StatusTooManyRequests, map[string]any{
		"code":       "locked",
		"message":    "locked",
		"retryAfter": retryAfter,
	})
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
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
	if !acct.totpEnrolled || strings.TrimSpace(acct.totpSecret) == "" {
		writeError(w, http.StatusUnauthorized, "totp_not_enrolled", "totp not enrolled")
		return
	}
	if !adminHashOK(req.Password, acct.passwordHash) {
		s.recordAdminLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "bad_password", "bad password")
		return
	}
	if !totp.Validate(acct.totpSecret, req.TOTP, s.now()) {
		s.recordAdminLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "bad_totp", "bad totp")
		return
	}
	s.clearAdminLoginFailure(ip)
	token, err := s.issueNamedToken(&s.adminTok)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token_error", "token error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) requireSiteAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminEnabled() {
			writeError(w, http.StatusUnauthorized, "admin_disabled", "admin disabled")
			return
		}
		token := bearerToken(r)
		if token == "" || !s.namedTokenOK("admin", token) {
			writeError(w, http.StatusUnauthorized, "need_auth", "need auth")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireSiteOrUserAdmin(next http.Handler) http.Handler {
	userAdmin := s.requireAuth(s.requireAdmin(next))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := bearerToken(r); tok != "" && s.namedTokenOK("admin", tok) {
			next.ServeHTTP(w, r)
			return
		}
		userAdmin.ServeHTTP(w, r)
	})
}

func (s *Server) namedTokenOK(kind, token string) bool {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.pruneAdminLocked()
	var m map[string]time.Time
	if kind == "setup" {
		m = s.setupTok
	} else {
		m = s.adminTok
	}
	exp, ok := m[token]
	return ok && !s.now().After(exp)
}

func (s *Server) pruneAdminLocked() {
	now := s.now()
	for tok, exp := range s.adminTok {
		if now.After(exp) {
			delete(s.adminTok, tok)
		}
	}
	for tok, exp := range s.setupTok {
		if now.After(exp) {
			delete(s.setupTok, tok)
		}
	}
	for ip, st := range s.adminFail {
		if st == nil {
			delete(s.adminFail, ip)
			continue
		}
		cut := now.Add(-adminLoginAttemptWindow)
		kept := st.attempts[:0]
		for _, t := range st.attempts {
			if t.After(cut) {
				kept = append(kept, t)
			}
		}
		st.attempts = kept
		if now.Before(st.lockedUntil) {
			continue
		}
		if len(st.attempts) == 0 && now.Sub(st.lastSeen) > adminFailPruneAfter {
			delete(s.adminFail, ip)
		}
	}
}

func (s *Server) adminLoginLocked(ip string) (retryAfter int, locked bool) {
	retry, blocked := s.adminLoginBlocked(ip)
	return retry, blocked
}

func (s *Server) adminLoginBlocked(ip string) (retryAfter int, locked bool) {
	now := s.now()
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.pruneAdminLocked()
	st := s.adminFail[ip]
	if st == nil {
		return 0, false
	}
	retry := 0
	if now.Before(st.lockedUntil) {
		retry = int(st.lockedUntil.Sub(now).Seconds())
	}
	if n := adminAttemptRetry(st.attempts, now); n > retry {
		retry = n
	}
	if retry < 1 {
		return 0, false
	}
	return retry, true
}

func adminAttemptRetry(attempts []time.Time, now time.Time) int {
	if len(attempts) < adminLoginMaxAttempts {
		return 0
	}
	sec := int(attempts[0].Add(adminLoginAttemptWindow).Sub(now).Seconds())
	if sec < 1 {
		return 1
	}
	return sec
}

func (s *Server) consumeAdminLoginAttempt(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	now := s.now()
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if s.adminFail == nil {
		s.adminFail = make(map[string]*adminLoginState)
	}
	s.pruneAdminLocked()
	st := s.adminFail[ip]
	if st == nil {
		st = &adminLoginState{}
		s.adminFail[ip] = st
	}
	st.lastSeen = now
	cut := now.Add(-adminLoginAttemptWindow)
	kept := st.attempts[:0]
	for _, t := range st.attempts {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	st.attempts = append(kept, now)
}

func (s *Server) recordAdminLoginFailure(ip string) {
	if ip == "" {
		ip = "unknown"
	}
	now := s.now()
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if s.adminFail == nil {
		s.adminFail = make(map[string]*adminLoginState)
	}
	s.pruneAdminLocked()
	st := s.adminFail[ip]
	if st == nil {
		st = &adminLoginState{}
		s.adminFail[ip] = st
	}
	if now.Before(st.lockedUntil) {
		return
	}
	st.failures++
	st.lastSeen = now
	if st.failures >= adminLoginFailLockAfter {
		st.lockedUntil = now.Add(adminLoginLockDuration(st.failures))
	}
}

func (s *Server) clearAdminLoginFailure(ip string) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	st := s.adminFail[ip]
	if st == nil {
		return
	}
	st.failures = 0
	st.lockedUntil = time.Time{}
}

func adminLoginLockDuration(failures int) time.Duration {
	shift := failures - adminLoginFailLockAfter
	if shift < 0 {
		return 0
	}
	mins := 1 << shift
	d := time.Duration(mins) * time.Minute
	if d > adminLoginLockMax {
		return adminLoginLockMax
	}
	return d
}

func clientIP(r *http.Request) string {
	if ip := parseClientIP(r.Header.Get("CF-Connecting-IP")); ip != "" {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if ip := parseClientIP(first); ip != "" {
			return ip
		}
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

func parseClientIP(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		v = host
	}
	ip := net.ParseIP(v)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func bearerToken(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Admin-Token"))
}

func adminHashOK(got, hash string) bool {
	if strings.TrimSpace(hash) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(got)) == nil
}

func (s *Server) snapshotAccount() (adminAccount, bool) {
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	if s.acct == nil {
		return adminAccount{}, false
	}
	return *s.acct, true
}

func (s *Server) replaceAccount(a adminAccount) {
	s.adminMu.Lock()
	cp := a
	s.acct = &cp
	s.adminMu.Unlock()
}

func (s *Server) persistAccount(ctx context.Context, a adminAccount) error {
	if s.store != nil {
		if err := s.store.UpsertAdminAccount(ctx, store.AdminAccount{
			PasswordHash: a.passwordHash,
			TOTPSecret:   a.totpSecret,
			TOTPPending:  a.totpPending,
			TOTPEnrolled: a.totpEnrolled,
		}); err != nil {
			return err
		}
	}
	s.replaceAccount(a)
	return nil
}

func (s *Server) adminEnabled() bool {
	a, ok := s.snapshotAccount()
	return ok && strings.TrimSpace(a.passwordHash) != ""
}

func (s *Server) totpEnrolled() bool {
	a, ok := s.snapshotAccount()
	return ok && a.totpEnrolled && strings.TrimSpace(a.totpSecret) != ""
}

func (s *Server) issueNamedToken(dst *map[string]time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	s.adminMu.Lock()
	defer s.adminMu.Unlock()
	s.pruneAdminLocked()
	if *dst == nil {
		*dst = make(map[string]time.Time)
	}
	(*dst)[token] = s.now().Add(adminTokenTTL)
	return token, nil
}

func (s *Server) clearPublicAdminSessions() {
	s.adminMu.Lock()
	s.adminTok = make(map[string]time.Time)
	s.adminMu.Unlock()
}

func (s *Server) EnsureAdmin(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	row, err := s.store.GetAdminAccount(ctx)
	if err == nil {
		s.replaceAccount(adminAccount{
			passwordHash: row.PasswordHash,
			totpSecret:   row.TOTPSecret,
			totpPending:  row.TOTPPending,
			totpEnrolled: row.TOTPEnrolled,
		})
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	seed := strings.TrimSpace(s.cfg.AdminPassword)
	if seed == "" {
		slog.Info("admin_account empty; public admin disabled until seeded")
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(seed), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := s.persistAccount(ctx, adminAccount{passwordHash: string(hash)}); err != nil {
		return err
	}
	slog.Info("admin_account seeded totp_enrolled=false")
	return nil
}

func (s *Server) adminPage(w http.ResponseWriter, r *http.Request) {
	setup := strings.TrimSpace(s.cfg.AdminSetupURL)
	if setup == "" {
		setup = "http://192.168.43.111:8080/admin/setup"
	}
	s.servePageEx(w, "admin.html", "", map[string]string{
		"__ADMIN_SETUP_URL__": setup,
	})
}

func (s *Server) adminSetupPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "admin-setup.html", "")
}
