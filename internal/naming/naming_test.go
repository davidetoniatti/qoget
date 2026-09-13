package naming_test

import (
	"strings"
	"testing"

	"github.com/davidetoniatti/qoget/internal/naming"
)

func TestExpand(t *testing.T) {
	vars := naming.Variables{
		"artist": "Some Band", "album": "First Album", "year": "2023",
		"bit_depth": "24", "sampling_rate": "96", "tracknumber": "01", "tracktitle": "Opening Track",
	}
	for _, tc := range []struct{ template, want string }{
		{naming.DefaultFolder, "Some Band - First Album (2023) [24B-96kHz]"},
		{naming.DefaultTrack, "01. Opening Track"},
		{"{Artist}/{album}", "Some Band-First Album"},
		{"{album} {missing}", "First Album"},
		{"{unclosed", "{unclosed"},
		{"", "Unknown"},
	} {
		if got := naming.Expand(tc.template, vars); got != tc.want {
			t.Errorf("Expand(%q) = %q, want %q", tc.template, got, tc.want)
		}
	}
}

func TestSanitize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Slash/Band", "Slash-Band"},
		{"Album Title: Live", "Album Title- Live"},
		{`What? "Yes" <no> | *maybe*`, "What_ _Yes_ _no_ _ _maybe_"},
		{"trailing dots...", "trailing dots"},
		{"  spaced   out  ", "spaced out"},
		{".hidden", "hidden"},
		{"tab\there", "tabhere"},
		{"Ïmaginary Ärtist — re:member", "Ïmaginary Ärtist — re-member"},
	} {
		if got := naming.Sanitize(tc.in); got != tc.want {
			t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	long := naming.Sanitize(strings.Repeat("é", 300))
	if len(long) > 200 || !strings.HasSuffix(long, "é") {
		t.Errorf("a long name is cut at a rune boundary: len=%d", len(long))
	}
}

func TestNumber(t *testing.T) {
	for _, tc := range []struct {
		position, total int
		want            string
	}{
		{1, 12, "01"}, {12, 12, "12"}, {7, 100, "007"}, {3, 0, "03"},
	} {
		if got := naming.Number(tc.position, tc.total); got != tc.want {
			t.Errorf("Number(%d, %d) = %q, want %q", tc.position, tc.total, got, tc.want)
		}
	}
}
