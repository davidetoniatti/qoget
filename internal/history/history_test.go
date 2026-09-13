package history_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidetoniatti/qoget/internal/history"
)

func TestHistoryRemembersAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "downloaded.txt")

	s, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Has("album", "a1") {
		t.Fatal("an empty history has nothing")
	}
	if err := s.Add("album", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("album", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("track", "t1"); err != nil {
		t.Fatal(err)
	}

	again, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Has("album", "a1") || !again.Has("track", "t1") || again.Has("track", "a1") {
		t.Errorf("reopened history lost entries")
	}
	if again.Len() != 2 {
		t.Errorf("Len = %d, want 2: a duplicate Add must not write twice", again.Len())
	}

	if err := history.Purge(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Purge should remove the file")
	}
	if err := history.Purge(path); err != nil {
		t.Errorf("purging twice: %v", err)
	}
}

func TestNilStoreIsHarmless(t *testing.T) {
	var s *history.Store
	if s.Has("album", "x") || s.Add("album", "x") != nil || s.Len() != 0 {
		t.Error("a nil store should remember nothing and fail nothing")
	}
}
