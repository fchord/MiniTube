package mailer

import (
	"context"
	"log/slog"
	"sync"
)

type Sender interface {
	SendVerification(ctx context.Context, email, token string) error
}

type Log struct {
	Log *slog.Logger
}

func (l Log) SendVerification(_ context.Context, email, token string) error {
	logger := l.Log
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("verification email (dev)", "email", email, "token", token)
	return nil
}

type Memory struct {
	mu     sync.Mutex
	tokens map[string]string
}

func NewMemory() *Memory {
	return &Memory{tokens: map[string]string{}}
}

func (m *Memory) SendVerification(_ context.Context, email, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[email] = token
	return nil
}

func (m *Memory) Token(email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tokens[email]
}
