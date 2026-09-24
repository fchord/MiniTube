package ident

import (
	"strings"
	"testing"
)

func TestNewVideoID(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 40; i++ {
		id := NewVideoID()
		if !ValidVideoID(id) {
			t.Fatalf("invalid %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate %q", id)
		}
		seen[id] = true
	}
	if ValidVideoID("short") || ValidVideoID("997c1363-5883-4a74-a78a-4cd06653aee9") || ValidVideoID("abcdefghij!") {
		t.Fatal("rejected ids must not pass")
	}
	if strings.ContainsAny(NewVideoID(), "-_") {
		t.Fatal("video id must be alphanumerics only")
	}
}

func TestNormalizeUsername(t *testing.T) {
	ok, err := NormalizeUsername("Alice_1")
	if err != nil || ok != "Alice_1" {
		t.Fatalf("got %q %v", ok, err)
	}
	if _, err := NormalizeUsername("ab"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := NormalizeUsername("1abc"); err == nil {
		t.Fatal("expected error")
	}
}

func TestNormalizeHandle(t *testing.T) {
	ok, err := NormalizeHandle("@Food")
	if err != nil || ok != "food" {
		t.Fatalf("got %q %v", ok, err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	ok, err := NormalizeEmail("  A@B.COM ")
	if err != nil || ok != "a@b.com" {
		t.Fatalf("got %q %v", ok, err)
	}
	if _, err := NormalizeEmail("not-an-email"); err == nil {
		t.Fatal("expected error")
	}
}
