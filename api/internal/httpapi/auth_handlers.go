package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"minitube/api/internal/auth"
	"minitube/api/internal/ident"
	"minitube/api/internal/store"
)

type registerReq struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Nickname string `json:"nickname"`
}

type loginReq struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

type verifyReq struct {
	Token string `json:"token"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	username, err := ident.NormalizeUsername(req.Username)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email, err := ident.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := ident.ValidPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	nickname, err := ident.NormalizeNickname(req.Nickname, username)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, err)
		return
	}
	emailTok, err := auth.RandomToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	refresh, err := auth.RandomToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	handle, err := ident.NormalizeHandle(username)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	taken, err := s.store.HandleTaken(r.Context(), handle)
	if err != nil {
		writeErr(w, err)
		return
	}
	if taken {
		handle = ident.UniqueHandle(handle)
	}

	now := time.Now()
	res, err := s.store.Register(r.Context(), store.RegisterInput{
		Username:     username,
		Email:        email,
		PasswordHash: hash,
		Nickname:     nickname,
		Handle:       handle,
		ChannelName:  nickname,
		RefreshHash:  auth.HashToken(refresh),
		UserAgent:    r.UserAgent(),
		SessionExp:   now.Add(s.cfg.RefreshTokenTTL),
		EmailHash:    auth.HashToken(emailTok),
		EmailExp:     now.Add(s.cfg.EmailTokenTTL),
	})
	if err != nil {
		writeConflictOr(w, err)
		return
	}
	if err := s.mail.SendVerification(r.Context(), email, emailTok); err != nil {
		writeErr(w, err)
		return
	}
	access, err := s.jwt.Issue(res.User.ID, res.Session.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusCreated, authResponseJSON{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         privateUser(res.User, res.Channel, &sub),
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	id := strings.TrimSpace(req.Identifier)
	if id == "" || req.Password == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials")
		return
	}
	u, err := s.store.GetUserByIdentifier(r.Context(), id)
	if err != nil || u.Status != "active" || !auth.CheckPassword(u.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "invalid credentials")
		return
	}
	refresh, err := auth.RandomToken()
	if err != nil {
		writeErr(w, err)
		return
	}
	sess, err := s.store.CreateSession(r.Context(), u.ID, auth.HashToken(refresh), r.UserAgent(), time.Now().Add(s.cfg.RefreshTokenTTL))
	if err != nil {
		writeErr(w, err)
		return
	}
	ch, err := s.store.DefaultChannel(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	access, err := s.jwt.Issue(u.ID, sess.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusOK, authResponseJSON{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         privateUser(u, ch, &sub),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess, ok := currentSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "unauthenticated")
		return
	}
	if err := s.store.RevokeSession(r.Context(), sess.ID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "token required")
		return
	}
	err := s.store.VerifyEmail(r.Context(), auth.HashToken(req.Token), time.Now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid or expired token")
			return
		}
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeConflictOr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrConflict) {
		msg := "already taken"
		es := err.Error()
		switch {
		case strings.Contains(es, "username"):
			msg = "username already taken"
		case strings.Contains(es, "email"):
			msg = "email already taken"
		case strings.Contains(es, "handle"):
			msg = "handle already taken"
		}
		writeError(w, http.StatusConflict, "conflict", msg)
		return
	}
	writeErr(w, err)
}
