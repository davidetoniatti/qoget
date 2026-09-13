// Package qobuztest stands in for Qobuz in tests: the player bundle it publishes, the
// catalogue it knows, the streams it serves, and a count of what was asked of it.
package qobuztest

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidetoniatti/qoget/internal/qobuz"
)

// Track is one track of a stubbed album.
type Track struct {
	ID         int64
	Title      string
	Version    string
	Seconds    int
	Position   int
	Medium     int
	Performer  string // defaults to the album artist
	Composer   string
	Restricted bool // the account may not stream it
}

// Album is one album the stubbed API knows about.
type Album struct {
	ID            string
	Title         string
	Version       string
	Artist        string
	ArtistID      int64
	Label         string
	Genre         string
	Date          string
	UPC           string
	ReleaseType   string
	MaxBitDepth   int
	MaxSampleRate float64
	Unstreamable  bool
	NoHiRes       bool
	Tracks        []Track

	// ClaimedTracks and ClaimedTotal override what the album says about itself, for the
	// cases where Qobuz's claim and its listing disagree.
	ClaimedTracks int
	ClaimedTotal  int
	OmitListing   bool
	NumericID     bool
}

func (a Album) fields() map[string]any {
	depth, rate := a.MaxBitDepth, a.MaxSampleRate
	if depth == 0 {
		depth = 24
	}
	if rate == 0 {
		rate = 96
	}
	claimed := a.ClaimedTracks
	if claimed == 0 {
		claimed = len(a.Tracks)
	}

	mediums := map[int]struct{}{}
	for _, track := range a.Tracks {
		mediums[track.Medium] = struct{}{}
	}

	var id any = a.ID
	if a.NumericID {
		// Qobuz quotes album ids in some responses and not in others.
		id = json.Number(a.ID)
	}
	artistID := a.ArtistID
	if artistID == 0 {
		artistID = 50505
	}
	upc := a.UPC
	if upc == "" {
		upc = a.ID
	}

	return map[string]any{
		"id":                     id,
		"title":                  a.Title,
		"version":                a.Version,
		"artist":                 map[string]any{"name": a.Artist, "id": artistID, "slug": slug(a.Artist)},
		"label":                  map[string]any{"name": a.Label},
		"genre":                  map[string]any{"name": a.Genre},
		"release_date_original":  a.Date,
		"release_type":           a.ReleaseType,
		"tracks_count":           claimed,
		"media_count":            max(len(mediums), 1),
		"duration":               3600,
		"maximum_bit_depth":      depth,
		"maximum_sampling_rate":  rate,
		"streamable":             !a.Unstreamable,
		"hires":                  !a.NoHiRes,
		"hires_streamable":       !a.NoHiRes,
		"parental_warning":       false,
		"upc":                    upc,
		"maximum_channel_count":  2,
		"release_date_stream":    a.Date,
		"release_date_download":  a.Date,
		"product_type":           "album",
		"purchasable":            true,
		"displayable":            true,
		"previewable":            true,
		"sampleable":             true,
		"downloadable":           true,
		"popularity":             0,
		"qobuz_id":               10000000,
		"maximum_technical_spec": "",
	}
}

func slug(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, " ", "-"))
}

// searchJSON is an album as a search result: everything but the track listing.
func (a Album) searchJSON() map[string]any { return a.fields() }

func (a Album) trackJSON(track Track) map[string]any {
	performer := track.Performer
	if performer == "" {
		performer = a.Artist
	}
	item := map[string]any{
		"id":           track.ID,
		"title":        track.Title,
		"version":      track.Version,
		"duration":     track.Seconds,
		"track_number": track.Position,
		"media_number": track.Medium,
		"isrc":         fmt.Sprintf("GBBKS17%05d", track.Position),
		"copyright":    "(P) " + a.Label,
		"streamable":   !track.Restricted,
		"performer":    map[string]any{"name": performer, "id": 50505},
	}
	if track.Composer != "" {
		item["composer"] = map[string]any{"name": track.Composer}
	}
	return item
}

// albumJSON is an album as album/get returns it, with its listing.
func (a Album) albumJSON() map[string]any {
	fields := a.fields()
	if a.OmitListing {
		return fields
	}

	items := make([]map[string]any, 0, len(a.Tracks))
	for _, track := range a.Tracks {
		items = append(items, a.trackJSON(track))
	}

	total := a.ClaimedTotal
	if total == 0 {
		total = len(items)
	}
	fields["tracks"] = map[string]any{"offset": 0, "limit": 500, "total": total, "items": items}
	return fields
}

// Playlist is one playlist the stub knows about, as (album id, track id) pairs in order.
type Playlist struct {
	ID     string
	Name   string
	Owner  string
	Tracks [][2]string
}

// Stub stands in for the Qobuz API.
type Stub struct {
	AppID string
	// Secrets are what the bundle publishes. Only ValidSecret signs successfully, which is
	// how the order they are tried in becomes observable.
	Secrets     []string
	ValidSecret string

	// Search is what album/search answers, in order; Albums is everything album/get knows.
	Search    []Album
	Albums    map[string]Album
	Playlists map[string]Playlist

	// ArtistName and LabelName are what artist/get and label/get call themselves; both list
	// every album in Albums whose id is in ArtistAlbums / LabelAlbums.
	ArtistName   string
	ArtistAlbums []string
	LabelName    string
	LabelAlbums  []string

	// UserID is what user/get answers with when the request carries a token.
	UserID string

	// File overrides the getFileUrl response for a correctly signed request.
	File func(query url.Values) (int, map[string]any)

	// Bundle and LoginRaw override the player bundle and the login page, for the cases
	// where they cannot be read.
	Bundle   *string
	LoginRaw *string

	// BaseURL is the stub's own address, so that a resolved stream URL and an album's
	// artwork point back at it rather than at Qobuz.
	BaseURL string

	// NoLargeImage refuses the cover sizes the API does not publish, which is the case the
	// fallback exists for.
	NoLargeImage bool

	// StreamFailures is how many times each stream URL answers 503 before serving the file,
	// StreamForbidden how many times it answers 403 (an expired signed URL), and StreamStalls
	// how many times it sends one byte and then nothing.
	StreamFailures  int
	StreamForbidden int
	StreamStalls    int

	mu       sync.Mutex
	requests map[string]int
}

// New builds a stub that knows the given albums and answers every search with them.
func New(albums ...Album) *Stub {
	stub := &Stub{
		AppID:       "798273057",
		Secrets:     []string{"b3a1f0e9c2d4a5b6c7d8e9f0a1b2c3d4"},
		ValidSecret: "b3a1f0e9c2d4a5b6c7d8e9f0a1b2c3d4",
		Search:      albums,
		Albums:      map[string]Album{},
		Playlists:   map[string]Playlist{},
		UserID:      "4242",
		requests:    map[string]int{},
	}
	for _, album := range albums {
		stub.Albums[album.ID] = album
	}
	return stub
}

// Start serves the stub and returns a client pointed at it.
func (s *Stub) Start(t testing.TB, opts qobuz.Options) *qobuz.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(server.Close)
	s.BaseURL = server.URL
	opts.APIURL = server.URL + "/api.json/0.2"
	opts.PlayURL = server.URL
	return qobuz.New(opts)
}

// Count is how many requests the stub has seen for one path.
func (s *Stub) Count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[path]
}

func (s *Stub) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests[r.URL.Path]++
	s.mu.Unlock()
	query := r.URL.Query()

	switch {
	case r.URL.Path == "/login":
		if s.LoginRaw != nil {
			_, _ = w.Write([]byte(*s.LoginRaw))
			return
		}
		_, _ = w.Write([]byte(`<html><head><script src="/resources/8.2.0/bundle.js"></script></head></html>`))

	case strings.HasSuffix(r.URL.Path, "bundle.js"):
		if s.Bundle != nil {
			_, _ = w.Write([]byte(*s.Bundle))
			return
		}
		_, _ = w.Write([]byte(PlayerBundle(s.AppID, s.Secrets...)))

	case strings.HasPrefix(r.URL.Path, "/stream/"):
		// handle has already counted this request, so the first attempt sees 1.
		switch attempt := s.Count(r.URL.Path); {
		case attempt <= s.StreamFailures:
			http.Error(w, "the edge is unwell", http.StatusServiceUnavailable)
		case attempt <= s.StreamFailures+s.StreamForbidden:
			http.Error(w, "signature expired", http.StatusForbidden)
		case attempt <= s.StreamFailures+s.StreamForbidden+s.StreamStalls:
			w.Header().Set("Content-Length", strconv.Itoa(len(FixtureBytes("blank.flac"))))
			_, _ = w.Write([]byte("f"))
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-time.After(30 * time.Second):
			}
		default:
			_, _ = w.Write(FixtureBytes("blank.flac"))
		}

	case strings.HasPrefix(r.URL.Path, "/image/"):
		if strings.Contains(r.URL.Path, "_max.jpg") || strings.Contains(r.URL.Path, "_org.jpg") {
			if s.NoLargeImage {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(LargeCoverBytes())
			return
		}
		_, _ = w.Write(FixtureBytes("cover.jpg"))

	case strings.HasSuffix(r.URL.Path, "/user/get"):
		if r.Header.Get("X-User-Auth-Token") == "" {
			WriteJSON(w, http.StatusUnauthorized, map[string]any{
				"status": "error", "code": 401, "message": "User authentication is required.",
			})
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"id": json.Number(s.UserID), "email": "user@example.com", "display_name": "Listener",
			"country_code": "IT",
			"subscription": map[string]any{"offer": "studio", "periodicity": "annual", "end_date": "2027-12-26", "is_canceled": false},
			"credential":   map[string]any{"label": "Qobuz Studio", "description": "Qobuz Studio"},
		})

	case strings.HasSuffix(r.URL.Path, "/album/search"):
		items := make([]map[string]any, 0, len(s.Search))
		for _, album := range s.Search {
			items = append(items, s.withImage(album.searchJSON(), album.ID))
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"query":  query.Get("query"),
			"albums": map[string]any{"limit": 15, "offset": 0, "total": len(items), "items": items},
		})

	case strings.HasSuffix(r.URL.Path, "/album/get"):
		album, known := s.Albums[query.Get("album_id")]
		if !known {
			WriteJSON(w, http.StatusNotFound, map[string]any{
				"status": "error", "code": 404, "message": "Album not found",
			})
			return
		}
		WriteJSON(w, http.StatusOK, s.withImage(album.albumJSON(), album.ID))

	case strings.HasSuffix(r.URL.Path, "/track/get"):
		album, track, found := s.findTrack(query.Get("track_id"))
		if !found {
			WriteJSON(w, http.StatusNotFound, map[string]any{
				"status": "error", "code": 404, "message": "Track not found",
			})
			return
		}
		item := album.trackJSON(track)
		item["album"] = s.withImage(album.searchJSON(), album.ID)
		WriteJSON(w, http.StatusOK, item)

	case strings.HasSuffix(r.URL.Path, "/track/search"):
		var items []map[string]any
		for _, album := range s.Search {
			for _, track := range album.Tracks {
				item := album.trackJSON(track)
				item["album"] = s.withImage(album.searchJSON(), album.ID)
				items = append(items, item)
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"tracks": map[string]any{"limit": 15, "offset": 0, "total": len(items), "items": items},
		})

	case strings.HasSuffix(r.URL.Path, "/artist/search"):
		WriteJSON(w, http.StatusOK, map[string]any{
			"artists": map[string]any{"items": []map[string]any{{"id": 50505, "name": s.ArtistName}}},
		})

	case strings.HasSuffix(r.URL.Path, "/artist/get"):
		WriteJSON(w, http.StatusOK, map[string]any{
			"id": 50505, "name": s.ArtistName,
			"albums": s.listing(s.ArtistAlbums, query),
		})

	case strings.HasSuffix(r.URL.Path, "/label/get"):
		WriteJSON(w, http.StatusOK, map[string]any{
			"id": 7890, "name": s.LabelName,
			"albums": s.listing(s.LabelAlbums, query),
		})

	case strings.HasSuffix(r.URL.Path, "/playlist/search"):
		var items []map[string]any
		for _, p := range s.Playlists {
			items = append(items, map[string]any{"id": json.Number(p.ID), "name": p.Name, "owner": map[string]any{"name": p.Owner}})
		}
		WriteJSON(w, http.StatusOK, map[string]any{"playlists": map[string]any{"items": items}})

	case strings.HasSuffix(r.URL.Path, "/playlist/get"):
		p, known := s.Playlists[query.Get("playlist_id")]
		if !known {
			WriteJSON(w, http.StatusNotFound, map[string]any{
				"status": "error", "code": 404, "message": "Playlist not found",
			})
			return
		}
		items := make([]map[string]any, 0, len(p.Tracks))
		for _, pair := range p.Tracks {
			album := s.Albums[pair[0]]
			for _, track := range album.Tracks {
				if strconv.FormatInt(track.ID, 10) == pair[1] {
					item := album.trackJSON(track)
					item["album"] = s.withImage(album.searchJSON(), album.ID)
					items = append(items, item)
				}
			}
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"id": json.Number(p.ID), "name": p.Name, "owner": map[string]any{"name": p.Owner},
			"tracks": map[string]any{"offset": 0, "limit": 500, "total": len(items), "items": items},
		})

	case strings.HasSuffix(r.URL.Path, "/track/getFileUrl"):
		s.serveFileURL(w, r)

	default:
		http.NotFound(w, r)
	}
}

// listing pages a set of album ids the way artist/get and label/get do.
func (s *Stub) listing(ids []string, query url.Values) map[string]any {
	offset, _ := strconv.Atoi(query.Get("offset"))
	limit, _ := strconv.Atoi(query.Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	items := []map[string]any{}
	for i := offset; i < len(ids) && i < offset+limit; i++ {
		album := s.Albums[ids[i]]
		items = append(items, s.withImage(album.searchJSON(), album.ID))
	}
	return map[string]any{"offset": offset, "limit": limit, "total": len(ids), "items": items}
}

func (s *Stub) findTrack(id string) (Album, Track, bool) {
	for _, album := range s.Albums {
		for _, track := range album.Tracks {
			if strconv.FormatInt(track.ID, 10) == id {
				return album, track, true
			}
		}
	}
	return Album{}, Track{}, false
}

func (s *Stub) serveFileURL(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !SignedWith(query, s.ValidSecret) {
		WriteJSON(w, http.StatusBadRequest, map[string]any{
			"status": "error", "code": 400, "message": "Invalid Request Signature",
		})
		return
	}

	if s.File != nil {
		status, body := s.File(query)
		WriteJSON(w, status, body)
		return
	}

	format := MustAtoi(query.Get("format_id"))
	extension := "flac"
	if format == int(qobuz.FormatMP3) {
		extension = "mp3"
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"track_id":      query.Get("track_id"),
		"url":           s.BaseURL + "/stream/" + query.Get("track_id") + "." + extension,
		"format_id":     format,
		"mime_type":     "audio/" + extension,
		"bit_depth":     24,
		"sampling_rate": 96,
	})
}

// withImage gives an album the cover URL Qobuz publishes with it, pointing at the stub.
func (s *Stub) withImage(body map[string]any, id string) map[string]any {
	// The sizes are named the way Qobuz names them, since what the API publishes as "large"
	// is not the largest it serves.
	body["image"] = map[string]any{
		"large":     s.BaseURL + "/image/" + id + "_600.jpg",
		"small":     s.BaseURL + "/image/" + id + "_230.jpg",
		"thumbnail": s.BaseURL + "/image/" + id + "_50.jpg",
	}
	return body
}

// LargeCoverBytes is the cover under the names the API does not publish, told apart from the
// published one by its size, which is the whole point of preferring it.
func LargeCoverBytes() []byte {
	return append(FixtureBytes("cover.jpg"), make([]byte, 4096)...)
}

// FixtureBytes is what the stub serves for a stream or an image.
func FixtureBytes(name string) []byte {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", name))
	if err != nil {
		panic("qobuztest: fixture is missing: " + err.Error())
	}
	return data
}

// SignedWith recomputes the signature Qobuz expects.
func SignedWith(query url.Values, secret string) bool {
	toSign := fmt.Sprintf("trackgetFileUrlformat_id%sintentstreamtrack_id%s%s%s",
		query.Get("format_id"), query.Get("track_id"), query.Get("request_ts"), secret)
	digest := md5.Sum([]byte(toSign))
	return hex.EncodeToString(digest[:]) == query.Get("request_sig")
}

// PlayerBundle lays out a player bundle the way the real one carries its app id and the
// fragments of each secret.
func PlayerBundle(appID string, secrets ...string) string {
	var b strings.Builder
	b.WriteString(`!function(e){var t={};`)
	fmt.Fprintf(&b, `production:{api:{appId:"%s",appSecret:"%s"}},`, appID, strings.Repeat("a", 32))

	zones := []string{"berlin", "london", "algier"}
	for i, secret := range secrets {
		zone := zones[i%len(zones)]
		seed, info, extras := splitSecret(secret)
		fmt.Fprintf(&b, `n.initialSeed("%s",window.utimezone.%s),`, seed, zone)
		fmt.Fprintf(&b, `{offset:"%d",name:"Europe/%s",info:"%s",extras:"%s"},`, i, zone, info, extras)
	}
	b.WriteString(`}(window);`)
	return b.String()
}

// splitSecret is the reverse of how a secret is assembled: base64, padded with the tail the
// player appends, then cut into three fragments.
func splitSecret(secret string) (seed, info, extras string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(secret))
	if !regexp.MustCompile(`^[\w=]+$`).MatchString(encoded) {
		panic("test secret does not survive the bundle's own character set: " + secret)
	}
	padded := encoded + strings.Repeat("x", 44)
	return padded[:10], padded[10:30], padded[30:]
}

// WriteJSON answers with a JSON body.
func WriteJSON(w http.ResponseWriter, status int, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// MustAtoi reads a number out of a query value, zero when there is none.
func MustAtoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
