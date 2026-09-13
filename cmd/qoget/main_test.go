package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/davidetoniatti/qoget/internal/link"
)

func TestSelection(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []int
	}{
		{"1", []int{0}},
		{"1,3-5", []int{0, 2, 3, 4}},
		{"2 2 1", []int{1, 0}},
		{"all", []int{0, 1, 2, 3, 4}},
		{"q", nil},
		{"", nil},
	} {
		got, err := selection(tc.in, 5)
		if err != nil {
			t.Errorf("selection(%q): %v", tc.in, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("selection(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	for _, in := range []string{"0", "6", "3-2", "x", "1,,y"} {
		if _, err := selection(in, 5); err == nil {
			t.Errorf("selection(%q) should have failed", in)
		}
	}
}

func TestResolveTargetsReadsFilesAndURLs(t *testing.T) {
	list := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(list, []byte("# comment\nhttps://open.qobuz.com/album/a1\n\ntrack:t1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	links, err := resolveTargets([]string{"https://www.qobuz.com/it-it/interpreter/some-artist/123456", list})
	if err != nil {
		t.Fatal(err)
	}
	want := []link.Link{{Kind: link.Artist, ID: "123456"}, {Kind: link.Album, ID: "a1"}, {Kind: link.Track, ID: "t1"}}
	if len(links) != len(want) {
		t.Fatalf("links = %+v", links)
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("links[%d] = %+v, want %+v", i, links[i], want[i])
		}
	}

	if _, err := resolveTargets([]string{"https://example.com/x"}); err == nil {
		t.Error("a foreign URL that is not a file should be refused")
	}
	bad := filepath.Join(t.TempDir(), "bad.txt")
	_ = os.WriteFile(bad, []byte("https://open.qobuz.com/album/a1\nnot a url\n"), 0o644)
	if _, err := resolveTargets([]string{bad}); err == nil || !strings.Contains(err.Error(), "bad.txt:2") {
		t.Errorf("a bad line should be reported with its number: %v", err)
	}
}

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	var out strings.Builder
	if err := run(nil, strings.NewReader(""), &out, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Commands:") {
		t.Errorf("usage = %q", out.String())
	}
	if err := run([]string{"nonsense"}, strings.NewReader(""), &out, &out); err == nil {
		t.Error("an unknown command should fail")
	}
}
