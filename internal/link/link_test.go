package link_test

import (
	"errors"
	"testing"

	"github.com/davidetoniatti/qoget/internal/link"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want link.Link
	}{
		{"https://www.qobuz.com/it-it/album/some-album-some-artist/abcdefghijklm", link.Link{Kind: link.Album, ID: "abcdefghijklm"}},
		{"https://www.qobuz.com/it-it/album/another-album-another-artist/0000000000003", link.Link{Kind: link.Album, ID: "0000000000003"}},
		{"https://www.qobuz.com/us-en/album/third-album-third-artist/0000000000001?foo=bar", link.Link{Kind: link.Album, ID: "0000000000001"}},
		{"https://open.qobuz.com/album/abcdefghijklm", link.Link{Kind: link.Album, ID: "abcdefghijklm"}},
		{"https://play.qobuz.com/album/abcdefghijklm", link.Link{Kind: link.Album, ID: "abcdefghijklm"}},
		{"www.qobuz.com/it-it/album/x/abc", link.Link{Kind: link.Album, ID: "abc"}},
		{"https://www.qobuz.com/it-it/interpreter/some-artist/123456", link.Link{Kind: link.Artist, ID: "123456"}},
		{"https://open.qobuz.com/artist/123456", link.Link{Kind: link.Artist, ID: "123456"}},
		{"https://www.qobuz.com/it-it/label/some-label/7890", link.Link{Kind: link.Label, ID: "7890"}},
		{"https://open.qobuz.com/track/200000001", link.Link{Kind: link.Track, ID: "200000001"}},
		{"https://www.qobuz.com/it-it/track/200000001", link.Link{Kind: link.Track, ID: "200000001"}},
		{"https://open.qobuz.com/playlist/3000002", link.Link{Kind: link.Playlist, ID: "3000002"}},
		{"https://www.qobuz.com/it-it/playlist/some-playlist/3000001", link.Link{Kind: link.Playlist, ID: "3000001"}},
		{"album:abcdefghijklm", link.Link{Kind: link.Album, ID: "abcdefghijklm"}},
		{"track:200000001", link.Link{Kind: link.Track, ID: "200000001"}},
	} {
		got, err := link.Parse(tc.in)
		if err != nil {
			t.Errorf("Parse(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseRefusesWhatItCannotUse(t *testing.T) {
	for _, in := range []string{
		"",
		"https://www.qobuz.com/it-it/",
		"https://www.qobuz.com/it-it/album",
		"https://www.qobuz.com/it-it/magazine/some-article/123",
		"genre:rock",
	} {
		if _, err := link.Parse(in); err == nil {
			t.Errorf("Parse(%q) should have failed", in)
		}
	}
	_, err := link.Parse("https://open.spotify.com/album/abc")
	if !errors.Is(err, link.ErrNotQobuz) {
		t.Errorf("a Spotify URL: error = %v, want ErrNotQobuz", err)
	}
}
