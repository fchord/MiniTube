package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
	ctxSession
)

func (s *Server) optionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := requestToken(r)
		if tok == "" {
			next.ServeHTTP(w, r)
			return
		}
		r, err := s.withAuth(r, tok)
		if err != nil {
			writeErr(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := requestToken(r)
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
			return
		}
		r, err := s.withAuth(r, tok)
		if err != nil {
			writeErr(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if raw, ok := strings.CutPrefix(h, "Bearer "); ok {
		if t := strings.TrimSpace(raw); t != "" {
			return t
		}
	}
	c, err := r.Cookie("mt_access")
	if err != nil || c.Value == "" {
		return ""
	}
	if v, err := url.QueryUnescape(c.Value); err == nil && v != "" {
		return v
	}
	return c.Value
}

func (s *Server) withAuth(r *http.Request, raw string) (*http.Request, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, apiError{status: http.StatusUnauthorized, code: "unauthenticated", message: "unauthenticated"}
	}
	claims, err := s.jwt.Parse(raw)
	if err != nil {
		return nil, apiError{status: http.StatusUnauthorized, code: "unauthenticated", message: "unauthenticated"}
	}
	sess, err := s.store.GetSession(r.Context(), claims.SessionID)
	if err != nil || sess.UserID != claims.UserID || sess.RevokedAt != nil || time.Now().After(sess.ExpiresAt) {
		return nil, apiError{status: http.StatusUnauthorized, code: "unauthenticated", message: "unauthenticated"}
	}
	user, err := s.store.GetUserByID(r.Context(), claims.UserID)
	if err != nil || user.Status != "active" {
		return nil, apiError{status: http.StatusUnauthorized, code: "unauthenticated", message: "unauthenticated"}
	}
	ctx := context.WithValue(r.Context(), ctxUser, user)
	ctx = context.WithValue(ctx, ctxSession, sess)
	return r.WithContext(ctx), nil
}

func currentUser(r *http.Request) (store.User, bool) {
	u, ok := r.Context().Value(ctxUser).(store.User)
	return u, ok
}

func currentSession(r *http.Request) (store.Session, bool) {
	s, ok := r.Context().Value(ctxSession).(store.Session)
	return s, ok
}

func viewerID(r *http.Request) *uuid.UUID {
	u, ok := currentUser(r)
	if !ok {
		return nil
	}
	id := u.ID
	return &id
}
