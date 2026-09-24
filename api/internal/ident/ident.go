package ident

import (
	"crypto/rand"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

const VideoIDLen = 10
const videoIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

var (
	nameRE    = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{2,31}$`)
	videoIDRE = regexp.MustCompile(`^[A-Za-z0-9]{10}$`)
)

func ValidVideoID(s string) bool {
	return videoIDRE.MatchString(s)
}

func NewVideoID() string {
	out := make([]byte, VideoIDLen)
	buf := make([]byte, VideoIDLen)
	for {
		if _, err := rand.Read(buf); err != nil {
			panic("video id: " + err.Error())
		}
		ok := true
		for i := 0; i < VideoIDLen; i++ {
			if buf[i] >= 248 {
				ok = false
				break
			}
			out[i] = videoIDAlphabet[int(buf[i])%62]
		}
		if ok {
			return string(out)
		}
	}
}

func NormalizeUsername(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !nameRE.MatchString(s) {
		return "", fmt.Errorf("username must be 3-32 chars, start with a letter, and contain only letters, digits, underscore")
	}
	return s, nil
}

func NormalizeHandle(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "@")
	s = strings.ToLower(s)
	if !nameRE.MatchString(s) {
		return "", fmt.Errorf("handle must be 3-32 chars, start with a letter, and contain only letters, digits, underscore")
	}
	return s, nil
}

func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address == "" || !strings.Contains(addr.Address, ".") {
		return "", fmt.Errorf("invalid email")
	}
	return strings.ToLower(addr.Address), nil
}

func NormalizeNickname(s, fallback string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = fallback
	}
	n := utf8.RuneCountInString(s)
	if n > 64 {
		return "", fmt.Errorf("nickname too long")
	}
	return s, nil
}

func ValidPassword(s string) error {
	if len(s) < 8 || len(s) > 128 {
		return fmt.Errorf("password must be 8-128 characters")
	}
	return nil
}

func ValidBio(s string) error {
	if utf8.RuneCountInString(s) > 2000 {
		return fmt.Errorf("bio too long")
	}
	return nil
}

func ValidChannelName(s string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(s))
	if n < 1 || n > 64 {
		return fmt.Errorf("channel name must be 1-64 characters")
	}
	return nil
}

func UniqueHandle(base string) string {
	suffix := make([]byte, 2)
	_, _ = rand.Read(suffix)
	if len(base) > 27 {
		base = base[:27]
	}
	return fmt.Sprintf("%s_%x", base, suffix)
}
