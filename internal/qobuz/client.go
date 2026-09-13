// Package qobuz talks to Qobuz: the catalogue, the account, and the streams an account may
// download.
//
// Every call needs the app id the web player publishes and an account token; a stream URL also
// needs one of the player's secrets. Search requires a token too (401 without one), so an
// unconfigured account cannot do anything here, not just download. Nothing outside this package
// needs to know any of that.
package qobuz

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultAPIURL  = "https://www.qobuz.com/api.json/0.2"
	defaultPlayURL = "https://play.qobuz.com"

	// The player bundle is served to browsers, and a request that does not look like one
	// gets a different page.
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:83.0) Gecko/20100101 Firefox/83.0"

	// listingLimit asks for a whole album in one response. Qobuz pages track listings, and
	// a listing that arrived truncated would be downloaded with tracks missing.
	listingLimit = 500

	// pageLimit is how many albums or tracks one page of an artist, label or playlist holds.
	pageLimit = 500

	defaultTimeout = 30 * time.Second

	// defaultStallTimeout is the gap that means a stream is gone. Anything still arriving
	// sends something inside it, however slowly; nothing arriving for half a minute has not
	// been seen to recover.
	defaultStallTimeout = 30 * time.Second

	// defaultAttempts and defaultRetryWait: three attempts, waiting 2s and then 4s, cover a
	// CDN edge having a bad moment without holding a queued album for minutes per track.
	defaultAttempts  = 3
	defaultRetryWait = 2 * time.Second
)

// ErrNoAuthToken means an account token was needed and none was configured.
var ErrNoAuthToken = errors.New("qobuz: an account token is required to resolve a stream URL")

// ErrUnavailable means Qobuz has no full-length file for the track: an account that may
// not stream it, or a catalogue entry that only offers a preview.
var ErrUnavailable = errors.New("qobuz: no full-length stream available")

// ErrWrongContainer means Qobuz offered the audio in a container other than the one the
// file was named for, which no later attempt can change.
var ErrWrongContainer = errors.New("qobuz: the stream is not the container the file was named for")

// Client talks to Qobuz's own API.
type Client struct {
	http *http.Client
	// stream has no overall timeout, because http.Client.Timeout bounds reading the body
	// too and would abort a large file part-way through.
	stream     *http.Client
	apiURL     string
	playURL    string
	userAgent  string
	authToken  string
	noFallback bool

	// stallTimeout is the gap between two reads of a stream that means it is gone rather
	// than slow; attempts and retryWait are how many times a file is fetched and how long
	// the first wait between attempts is.
	stallTimeout time.Duration
	attempts     int
	retryWait    time.Duration
	logger       *slog.Logger

	// mu guards the credentials read out of the player bundle. secret is the index of the
	// one that last signed successfully: secrets are tried in turn, and remembering the
	// winner keeps every later track from repeating the failures.
	mu      sync.Mutex
	appID   string
	secrets []string
	secret  int
}

// Options configures a Client.
type Options struct {
	// AuthToken is the account's X-User-Auth-Token. Nothing works without one except Login,
	// which is how one is obtained.
	AuthToken string

	// NoFallback refuses a lower quality than the one asked for instead of accepting the
	// best Qobuz will give in the same container.
	NoFallback bool

	// HTTPClient overrides the transport.
	HTTPClient *http.Client

	// APIURL and PlayURL override the service addresses, for tests.
	APIURL, PlayURL string

	// Timeout bounds an API call from start to finish. StreamTimeout bounds only getting a
	// stream started — connecting, the handshake, the response header — and deliberately
	// not reading it, since that is the part that takes as long as the file is long.
	// StallTimeout is what bounds the reading: the gap between two reads, not the whole.
	Timeout       time.Duration
	StreamTimeout time.Duration
	StallTimeout  time.Duration

	// Attempts is how many times one file is fetched before Fetch gives up, and RetryWait
	// the wait after the first failure; each later wait doubles.
	Attempts  int
	RetryWait time.Duration

	// Logger records a file being fetched again. Nothing else in this package logs.
	Logger *slog.Logger

	// AppID and Secrets skip reading the player bundle when they are already known.
	AppID   string
	Secrets []string

	UserAgent string
}

func New(opts Options) *Client {
	c := &Client{
		http:         opts.HTTPClient,
		apiURL:       strings.TrimSuffix(orDefault(opts.APIURL, defaultAPIURL), "/"),
		playURL:      strings.TrimSuffix(orDefault(opts.PlayURL, defaultPlayURL), "/"),
		userAgent:    orDefault(opts.UserAgent, defaultUserAgent),
		authToken:    opts.AuthToken,
		noFallback:   opts.NoFallback,
		appID:        opts.AppID,
		secrets:      opts.Secrets,
		stallTimeout: orDuration(opts.StallTimeout, defaultStallTimeout),
		attempts:     orPositive(opts.Attempts, defaultAttempts),
		retryWait:    orDuration(opts.RetryWait, defaultRetryWait),
		logger:       opts.Logger,
	}
	if c.logger == nil {
		c.logger = slog.New(slog.DiscardHandler)
	}
	if c.http != nil {
		c.stream = c.http
		return c
	}

	streamTimeout := orDuration(opts.StreamTimeout, defaultTimeout)
	c.http = &http.Client{Timeout: orDuration(opts.Timeout, defaultTimeout)}
	c.stream = &http.Client{Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: streamTimeout}).DialContext,
		TLSHandshakeTimeout:   streamTimeout,
		ResponseHeaderTimeout: streamTimeout,
	}}
	return c
}

// HasAuthToken reports whether the client carries an account token.
func (c *Client) HasAuthToken() bool { return c.authToken != "" }

// Credentials returns the app id and secrets in use, so a caller can keep them and skip
// reading the player bundle next time. Empty until something has needed them.
func (c *Client) Credentials() (appID string, secrets []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appID, append([]string(nil), c.secrets...)
}

func orDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func orPositive(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// Album is a Qobuz album. A search result carries everything but the track listing, which
// costs a request of its own.
type Album struct {
	ID      string
	Title   string
	Version string // "Deluxe Edition", "Remastered"; empty for most albums
	Artist  string
	Label   string
	Genre   string
	Date    string // ISO 8601
	UPC     string

	// ArtistID and ArtistSlug are the artist's own page on Qobuz, which arrives with the album.
	// Either can be empty on an album Qobuz credits to nobody in particular.
	ArtistID   string
	ArtistSlug string

	// TrackCount is what Qobuz claims, which is not necessarily what the listing contains.
	TrackCount int
	MediaCount int
	Duration   time.Duration

	MaxBitDepth   int
	MaxSampleRate float64

	// Image is the cover Qobuz publishes with the album, at the largest size it offers.
	Image string

	Streamable      bool
	HiResStreamable bool

	// ReleaseType is Qobuz's own classification: "album", "single", "ep", "compilation"...
	ReleaseType string

	// Tracks is empty on a search result and ordered by medium and position otherwise.
	Tracks []Track
}

// Year is the release year, or an empty string when Qobuz gave no date.
func (a *Album) Year() string {
	if len(a.Date) >= 4 {
		return a.Date[:4]
	}
	return ""
}

// FullTitle is the title with the version Qobuz keeps apart, the way the store displays it.
func (a *Album) FullTitle() string {
	if a.Version == "" {
		return a.Title
	}
	return a.Title + " (" + a.Version + ")"
}

// Track is one track of an album.
type Track struct {
	ID        string
	Title     string
	Version   string
	Position  int // within its medium
	Medium    int
	Duration  time.Duration
	ISRC      string
	Performer string // the track's own artist credit, which differs from the album's on compilations
	Composer  string
	Copyright string

	// Streamable is false for a track the account may not stream, which is not the same
	// as the album being unavailable: single tracks drop out of otherwise fine albums.
	Streamable bool

	// Album is set on a track read on its own (Track, Playlist) and nil inside Album.Tracks,
	// where the album is the one holding the listing.
	Album *Album
}

// FullTitle is the title with its version, the way the store displays it.
func (t *Track) FullTitle() string {
	if t.Version == "" {
		return t.Title
	}
	return t.Title + " (" + t.Version + ")"
}

// SearchAlbums returns albums matching a free-text query.
func (c *Client) SearchAlbums(ctx context.Context, query string, limit int) ([]Album, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("qobuz: empty search query")
	}
	if limit <= 0 {
		limit = 10
	}

	var response struct {
		Albums struct {
			Items []albumJSON `json:"items"`
		} `json:"albums"`
	}
	if err := c.get(ctx, "album/search", url.Values{
		"query":  {query},
		"limit":  {strconv.Itoa(limit)},
		"offset": {"0"},
	}, &response); err != nil {
		return nil, err
	}

	albums := make([]Album, 0, len(response.Albums.Items))
	for _, item := range response.Albums.Items {
		if item.ID == "" {
			// Nothing can be done with an album that cannot be looked up again.
			continue
		}
		albums = append(albums, item.album())
	}
	return albums, nil
}

// Album returns one album with its whole track listing.
func (c *Client) Album(ctx context.Context, id string) (*Album, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("qobuz: empty album id")
	}

	var item albumJSON
	if err := c.get(ctx, "album/get", url.Values{
		"album_id": {id},
		"limit":    {strconv.Itoa(listingLimit)},
		"offset":   {"0"},
	}, &item); err != nil {
		return nil, err
	}

	if item.Tracks == nil || len(item.Tracks.Items) == 0 {
		return nil, fmt.Errorf("qobuz: album %s came back without a track listing", id)
	}
	if item.Tracks.Total > len(item.Tracks.Items) {
		return nil, fmt.Errorf("qobuz: album %s listing is truncated at %d of %d tracks",
			id, len(item.Tracks.Items), item.Tracks.Total)
	}

	album := item.album()
	if album.ID == "" {
		album.ID = id
	}
	return &album, nil
}

// File is a resolved stream: a URL good for a limited time, and what it will contain.
type File struct {
	URL    string
	Format Format

	// BitDepth and SampleRate are what the stream actually carries, which is at most what
	// was asked for.
	BitDepth   int
	SampleRate float64
}

// FileURL resolves a signed stream URL for one track.
//
// Both the secret and the format may be refused, so the combinations are tried in turn,
// preferred format first. The formats offered all share one container, since the file has
// already been named by the time this runs, and the name carries the extension.
func (c *Client) FileURL(ctx context.Context, trackID string, preferred Format) (*File, error) {
	if strings.TrimSpace(trackID) == "" {
		return nil, errors.New("qobuz: empty track id")
	}
	if !preferred.Valid() {
		return nil, fmt.Errorf("qobuz: invalid format %s", preferred)
	}
	if c.authToken == "" {
		return nil, ErrNoAuthToken
	}
	if err := c.ensureBundle(ctx); err != nil {
		return nil, err
	}

	formats := preferred.fallbacks()
	if c.noFallback {
		formats = formats[:1]
	}

	var lastErr error
	for _, format := range formats {
		for offset := range c.secretCount() {
			index, secret := c.secretAt(offset)
			file, err := c.signedFileURL(ctx, trackID, format, secret)
			if err == nil {
				c.rememberSecret(index)
				return file, nil
			}
			if errors.Is(err, ErrUnavailable) {
				// The signature was accepted and the answer was no; another secret will
				// get the same answer.
				lastErr = err
				break
			}
			lastErr = err
		}
	}
	return nil, fmt.Errorf("qobuz: no stream URL for track %s: %w", trackID, lastErr)
}

func (c *Client) signedFileURL(ctx context.Context, trackID string, format Format, secret string) (*File, error) {
	timestamp := time.Now().Unix()

	// Qobuz signs the request parameters in a fixed order with the secret appended.
	toSign := fmt.Sprintf("trackgetFileUrlformat_id%dintentstreamtrack_id%s%d%s",
		int(format), trackID, timestamp, secret)
	digest := md5.Sum([]byte(toSign))

	var response struct {
		URL          string  `json:"url"`
		FormatID     int     `json:"format_id"`
		Sample       bool    `json:"sample"`
		BitDepth     int     `json:"bit_depth"`
		SamplingRate float64 `json:"sampling_rate"`
	}
	if err := c.get(ctx, "track/getFileUrl", url.Values{
		"format_id":   {strconv.Itoa(int(format))},
		"intent":      {"stream"},
		"track_id":    {trackID},
		"request_ts":  {strconv.FormatInt(timestamp, 10)},
		"request_sig": {hex.EncodeToString(digest[:])},
	}, &response); err != nil {
		return nil, err
	}

	if response.URL == "" {
		return nil, fmt.Errorf("qobuz: track %s: %w", trackID, ErrUnavailable)
	}
	if response.Sample {
		// A preview under the same field name as the real thing; taking it would save
		// thirty seconds of audio as an album track.
		return nil, fmt.Errorf("qobuz: track %s is only offered as a preview: %w",
			trackID, ErrUnavailable)
	}

	delivered := format
	if response.FormatID != 0 {
		delivered = Format(response.FormatID)
	}
	if delivered.Extension() != format.Extension() {
		// The file is already named, so a different container is unusable however good
		// the audio is.
		return nil, fmt.Errorf("qobuz: track %s came back as %s, not %s: %w",
			trackID, delivered, format, ErrWrongContainer)
	}

	return &File{
		URL: response.URL, Format: delivered,
		BitDepth: response.BitDepth, SampleRate: response.SamplingRate,
	}, nil
}

func (c *Client) open(ctx context.Context, url string) (io.ReadCloser, int64, error) {
	// The stream gets a context of its own so that giving up on a stalled body ends this
	// request and leaves the caller's untouched. Closing the body releases it.
	ctx, cancel := context.WithCancel(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.stream.Do(req)
	if err != nil {
		cancel()
		return nil, 0, fmt.Errorf("qobuz: stream: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, 0, &statusError{code: resp.StatusCode}
	}
	return newStallReader(resp.Body, cancel, c.stallTimeout), resp.ContentLength, nil
}

// Progress is told how much of a file has arrived. total is -1 when the server did not say.
type Progress func(written, total int64)

// Download writes a resolved file to path, through a temporary name beside it.
//
// A transfer that dies half way must not leave something there that looks finished: the
// rename is what makes the file appear whole or not at all. progress may be nil.
func (c *Client) Download(ctx context.Context, file *File, path string, progress Progress) (int64, error) {
	return c.fetch(ctx, file.URL, path, progress)
}

// CoverSize is which rendition of an album's artwork to fetch.
type CoverSize string

const (
	// CoverMax is the 1400px rendition, served under a name the API does not publish.
	CoverMax CoverSize = "max"
	// CoverOriginal is the artwork as the label supplied it, which can be very large.
	CoverOriginal CoverSize = "org"
	// CoverLarge is the 600px rendition the API publishes.
	CoverLarge CoverSize = "large"
)

// ParseCoverSize accepts the names above; an empty string means CoverMax.
func ParseCoverSize(s string) (CoverSize, error) {
	switch CoverSize(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return CoverMax, nil
	case CoverMax:
		return CoverMax, nil
	case CoverOriginal, "original":
		return CoverOriginal, nil
	case CoverLarge, "600":
		return CoverLarge, nil
	}
	return "", fmt.Errorf("qobuz: unknown cover size %q (max, org or large)", s)
}

// SaveImage writes an album's cover art to path, through the same temporary name. Uses the
// streaming client, not the API one: an image is a file being read, not a call being made.
//
// The API publishes a 600px cover but serves larger ones under undocumented names; the one
// asked for is tried first, falling back to the published URL, so relying on the
// undocumented convention never leaves us worse off than not knowing about it.
func (c *Client) SaveImage(ctx context.Context, url string, size CoverSize, path string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("qobuz: no cover art to save")
	}
	if size == "" {
		size = CoverMax
	}
	if size != CoverLarge {
		if larger := strings.Replace(url, "_600.jpg", "_"+string(size)+".jpg", 1); larger != url {
			if _, err := c.fetch(ctx, larger, path, nil); err == nil {
				return nil
			}
		}
	}
	_, err := c.fetch(ctx, url, path, nil)
	return err
}

func (c *Client) fetch(ctx context.Context, url, path string, progress Progress) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("qobuz: create %s: %w", filepath.Dir(path), err)
	}

	stream, total, err := c.open(ctx, url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = stream.Close() }()

	temporary := path + ".part"
	out, err := os.Create(temporary)
	if err != nil {
		return 0, fmt.Errorf("qobuz: create %s: %w", temporary, err)
	}

	var dst io.Writer = out
	if progress != nil {
		dst = &progressWriter{w: out, total: total, report: progress}
	}
	written, err := io.Copy(dst, stream)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temporary, path)
	}
	if err != nil {
		_ = os.Remove(temporary)
		return written, fmt.Errorf("qobuz: download to %s: %w", path, err)
	}
	return written, nil
}

type progressWriter struct {
	w       io.Writer
	written int64
	total   int64
	report  Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.written += int64(n)
	p.report(p.written, p.total)
	return n, err
}

// ensureBundle reads the app id and secrets from the player, once.
func (c *Client) ensureBundle(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.appID != "" && len(c.secrets) > 0 {
		return nil
	}

	b, err := fetchBundle(ctx, c.http, c.playURL, c.userAgent)
	if err != nil {
		return err
	}
	c.appID, c.secrets, c.secret = b.appID, b.secrets, 0
	return nil
}

// appIdentifier returns the app id, reading the player bundle if it is not known yet. The
// catalogue needs it and nothing else.
func (c *Client) appIdentifier(ctx context.Context) (string, error) {
	c.mu.Lock()
	appID := c.appID
	c.mu.Unlock()
	if appID != "" {
		return appID, nil
	}

	if err := c.ensureBundle(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appID, nil
}

func (c *Client) secretCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.secrets)
}

// secretAt returns the offset-th secret to try, starting from the one that last worked.
func (c *Client) secretAt(offset int) (int, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := (c.secret + offset) % len(c.secrets)
	return index, c.secrets[index]
}

func (c *Client) rememberSecret(index int) {
	c.mu.Lock()
	c.secret = index
	c.mu.Unlock()
}

// get calls the API and decodes the response into out.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	appID, err := c.appIdentifier(ctx)
	if err != nil {
		return err
	}
	query.Set("app_id", appID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.apiURL+"/"+path+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("qobuz: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-App-Id", appID)
	if c.authToken != "" {
		req.Header.Set("X-User-Auth-Token", c.authToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("qobuz: %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("qobuz: %s: read response: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return &statusError{path: path, code: resp.StatusCode, message: apiMessage(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("qobuz: %s: decode response: %w", path, err)
	}
	return nil
}

// apiMessage pulls the explanation out of an error response, falling back to the body.
func apiMessage(body []byte) string {
	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Message != "" {
		return envelope.Message
	}
	return strings.TrimSpace(string(body[:min(len(body), 256)]))
}

// --- wire types -------------------------------------------------------------------
//
// Kept apart from the exported ones because the API is loose about them: an album id is
// sometimes a number and sometimes a string, and durations are bare seconds.

type albumJSON struct {
	ID              flexString `json:"id"`
	Title           string     `json:"title"`
	Version         string     `json:"version"`
	Artist          *named     `json:"artist"`
	Label           *named     `json:"label"`
	Genre           *named     `json:"genre"`
	Date            string     `json:"release_date_original"`
	DateStream      string     `json:"release_date_stream"`
	UPC             string     `json:"upc"`
	TracksCount     int        `json:"tracks_count"`
	MediaCount      int        `json:"media_count"`
	Duration        int        `json:"duration"`
	MaxBitDepth     int        `json:"maximum_bit_depth"`
	MaxSampleRate   float64    `json:"maximum_sampling_rate"`
	Streamable      bool       `json:"streamable"`
	HiResStreamable bool       `json:"hires_streamable"`
	ReleaseType     string     `json:"release_type"`

	Image *struct {
		Large     string `json:"large"`
		Small     string `json:"small"`
		Thumbnail string `json:"thumbnail"`
	} `json:"image"`

	Tracks *struct {
		Total int         `json:"total"`
		Items []trackJSON `json:"items"`
	} `json:"tracks"`
}

type trackJSON struct {
	ID          flexString `json:"id"`
	Title       string     `json:"title"`
	Version     string     `json:"version"`
	Duration    int        `json:"duration"`
	TrackNumber int        `json:"track_number"`
	MediaNumber int        `json:"media_number"`
	ISRC        string     `json:"isrc"`
	Copyright   string     `json:"copyright"`
	Streamable  bool       `json:"streamable"`
	Performer   *named     `json:"performer"`
	Composer    *named     `json:"composer"`

	// Album is present when a track is read on its own or inside a playlist.
	Album *albumJSON `json:"album"`
}

type named struct {
	Name string `json:"name"`

	// ID and Slug compose the artist's page (/interpreter/<slug>/<id>).
	ID   flexString `json:"id"`
	Slug string     `json:"slug"`
}

func (n *named) name() string {
	if n == nil {
		return ""
	}
	return n.Name
}

func (n *named) id() string {
	if n == nil {
		return ""
	}
	return string(n.ID)
}

func (n *named) slug() string {
	if n == nil {
		return ""
	}
	return n.Slug
}

// coverURL prefers the largest artwork offered; the smaller sizes are thumbnails, and a
// thumbnail embedded in a file is worse than no artwork at all.
func (a albumJSON) coverURL() string {
	if a.Image == nil {
		return ""
	}
	for _, url := range []string{a.Image.Large, a.Image.Small, a.Image.Thumbnail} {
		if url != "" {
			return url
		}
	}
	return ""
}

func (a albumJSON) album() Album {
	album := Album{
		ID:              string(a.ID),
		Title:           a.Title,
		Version:         a.Version,
		Artist:          a.Artist.name(),
		ArtistID:        a.Artist.id(),
		ArtistSlug:      a.Artist.slug(),
		Label:           a.Label.name(),
		Genre:           a.Genre.name(),
		Date:            orDefault(a.Date, a.DateStream),
		UPC:             a.UPC,
		TrackCount:      a.TracksCount,
		MediaCount:      a.MediaCount,
		Duration:        time.Duration(a.Duration) * time.Second,
		MaxBitDepth:     a.MaxBitDepth,
		MaxSampleRate:   a.MaxSampleRate,
		Image:           a.coverURL(),
		Streamable:      a.Streamable,
		HiResStreamable: a.HiResStreamable,
		ReleaseType:     a.ReleaseType,
	}
	if a.Tracks == nil {
		return album
	}

	album.Tracks = make([]Track, 0, len(a.Tracks.Items))
	for _, item := range a.Tracks.Items {
		album.Tracks = append(album.Tracks, item.track())
	}

	// The response is usually in order already; sorting makes it so regardless, since the
	// files are numbered in the order they arrive in.
	sort.SliceStable(album.Tracks, func(i, j int) bool {
		if album.Tracks[i].Medium != album.Tracks[j].Medium {
			return album.Tracks[i].Medium < album.Tracks[j].Medium
		}
		return album.Tracks[i].Position < album.Tracks[j].Position
	})
	return album
}

func (item trackJSON) track() Track {
	medium := item.MediaNumber
	if medium == 0 {
		// Single-disc albums sometimes leave it out.
		medium = 1
	}
	track := Track{
		ID:         string(item.ID),
		Title:      item.Title,
		Version:    item.Version,
		Position:   item.TrackNumber,
		Medium:     medium,
		Duration:   time.Duration(item.Duration) * time.Second,
		ISRC:       item.ISRC,
		Copyright:  item.Copyright,
		Performer:  item.Performer.name(),
		Composer:   item.Composer.name(),
		Streamable: item.Streamable,
	}
	if item.Album != nil {
		album := item.Album.album()
		track.Album = &album
	}
	return track
}

// flexString is a JSON value that is sometimes quoted and sometimes not: album ids are
// barcodes, and Qobuz returns them either way.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return fmt.Errorf("qobuz: id is neither string nor number: %s", text)
	}
	*f = flexString(number.String())
	return nil
}
