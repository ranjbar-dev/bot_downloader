package store

import (
	"path/filepath"
	"testing"
)

func TestUpsertAndStatus(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "members.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, known, err := s.Status(1); err != nil || known {
		t.Fatalf("expected unknown user, got known=%v err=%v", known, err)
	}

	if err := s.Upsert(1, "member", 100); err != nil {
		t.Fatal(err)
	}
	if status, known, err := s.Status(1); err != nil || !known || status != "member" {
		t.Fatalf("expected member/known, got %q known=%v err=%v", status, known, err)
	}

	// A stale event (older Date than what's stored) must not overwrite.
	if err := s.Upsert(1, "left", 50); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := s.Status(1); status != "member" {
		t.Fatalf("stale update overwrote newer status, got %q", status)
	}

	// A newer event applies normally.
	if err := s.Upsert(1, "left", 150); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := s.Status(1); status != "left" {
		t.Fatalf("expected left, got %q", status)
	}
}
