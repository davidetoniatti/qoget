// Package link reads Qobuz URLs: which kind of thing they name and its id.
package link

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Kind is what a Qobuz URL points at.
type Kind string

const (
	Album    Kind = "album"
	Track    Kind = "track"
	Artist   Kind = "artist"
	Label    Kind = "label"
	Playlist Kind = "playlist"
)

// Link is a parsed Qobuz URL.
type Link struct {
	Kind Kind
	ID   string
}

// ErrNotQobuz means the URL is not on a Qobuz host.
var ErrNotQobuz = errors.New("link: not a Qobuz URL")

// Parse reads a Qobuz URL of any of the forms the store, the web player and the share
// buttons produce:
//
//	https://www.qobuz.com/it-it/album/<slug>/<id>
//	https://www.qobuz.com/it-it/interpreter/<slug>/<id>     (an artist)
//	https://www.qobuz.com/it-it/label/<slug>/<id>
//	https://www.qobuz.com/us-en/track/<id>
//	https://open.qobuz.com/album/<id>   https://play.qobuz.com/album/<id>
//	https://open.qobuz.com/artist/<id>  https://open.qobuz.com/playlist/<id>
//	https://www.qobuz.com/playlist/<id>
//
// A bare "<kind>:<id>" (album:abcdefghijklm, track:200000001) is accepted too, for things
// already known by id.
func Parse(raw string) (Link, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Link{}, errors.New("link: empty")
	}

	if kind, id, ok := strings.Cut(raw, ":"); ok && !strings.Contains(kind, "/") && !strings.HasPrefix(id, "//") {
		return withID(Kind(strings.ToLower(kind)), id)
	}

	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Link{}, fmt.Errorf("link: %w", err)
	}
	host := strings.ToLower(u.Hostname())
	if host != "qobuz.com" && !strings.HasSuffix(host, ".qobuz.com") {
		return Link{}, fmt.Errorf("%w: %s", ErrNotQobuz, u.Hostname())
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// The store puts a locale first (it-it, us-en); the player and open.qobuz.com do not.
	if len(parts) > 0 && isLocale(parts[0]) {
		parts = parts[1:]
	}
	if len(parts) < 2 {
		return Link{}, fmt.Errorf("link: %s names nothing to download", raw)
	}

	var kind Kind
	switch parts[0] {
	case "album":
		kind = Album
	case "track":
		kind = Track
	case "interpreter", "artist":
		kind = Artist
	case "label":
		kind = Label
	case "playlist":
		kind = Playlist
	default:
		return Link{}, fmt.Errorf("link: %s is not an album, track, artist, label or playlist", raw)
	}

	// The id is the last segment; the store puts a slug before it, the player does not.
	return withID(kind, parts[len(parts)-1])
}

func withID(kind Kind, id string) (Link, error) {
	switch kind {
	case Album, Track, Artist, Label, Playlist:
	default:
		return Link{}, fmt.Errorf("link: unknown kind %q", kind)
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.ContainsAny(id, "/?&# ") {
		return Link{}, fmt.Errorf("link: %q is not an id", id)
	}
	return Link{Kind: kind, ID: id}, nil
}

// isLocale recognises the store's language-country prefix ("it-it", "us-en", "fr-fr").
func isLocale(s string) bool {
	if len(s) != 5 || s[2] != '-' {
		return false
	}
	for i, r := range s {
		if i == 2 {
			continue
		}
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}
