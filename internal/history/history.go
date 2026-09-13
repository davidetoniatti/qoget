// Package history remembers what has been downloaded, so that an album or track asked for
// twice is skipped the second time. One line per entry, "<kind>:<id>", in a plain text file
// that is safe to read, edit or delete by hand.
package history

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Store is the file of downloaded ids. A nil *Store remembers nothing and skips nothing.
type Store struct {
	path string

	mu   sync.Mutex
	seen map[string]bool
}

// Open reads the file at path, creating its directory. A missing file is an empty history.
func Open(path string) (*Store, error) {
	s := &Store{path: path, seen: map[string]bool{}}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("history: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			s.seen[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("history: read %s: %w", path, err)
	}
	return s, nil
}

// Path is where the history lives.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Has reports whether kind:id has been downloaded before.
func (s *Store) Has(kind, id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[key(kind, id)]
}

// Add records kind:id, appending to the file at once so an interrupted run keeps what it did.
func (s *Store) Add(kind, id string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(kind, id)
	if s.seen[k] {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("history: %w", err)
	}
	_, err = f.WriteString(k + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("history: write %s: %w", s.path, err)
	}
	s.seen[k] = true
	return nil
}

// Len is how many entries the history holds.
func (s *Store) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// Purge deletes the file. A history that never existed is already purged.
func Purge(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func key(kind, id string) string { return kind + ":" + id }
