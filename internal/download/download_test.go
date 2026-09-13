package download_test

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-flac/flacvorbis"
	"github.com/go-flac/go-flac"

	"github.com/davidetoniatti/qoget/internal/download"
	"github.com/davidetoniatti/qoget/internal/history"
	"github.com/davidetoniatti/qoget/internal/qobuz"
	"github.com/davidetoniatti/qoget/internal/qobuz/qobuztest"
)

func firstAlbum() qobuztest.Album {
	return qobuztest.Album{
		ID: "abc123def456g", Title: "First Album", Artist: "Some Band", Label: "Some Label", Genre: "Metal",
		Date: "2023-03-03", UPC: "0000000000002", ReleaseType: "album",
		Tracks: []qobuztest.Track{
			{ID: 900000001, Title: "Opening Track", Seconds: 300, Position: 1, Medium: 1, Composer: "A Composer"},
			{ID: 900000002, Title: "Second Track", Seconds: 400, Position: 2, Medium: 1},
		},
	}
}

func boxSet() qobuztest.Album {
	return qobuztest.Album{
		ID: "box", Title: "Live Set", Version: "Deluxe Edition", Artist: "Slash/Band", Label: "Other Label", Date: "1992-10-27",
		Tracks: []qobuztest.Track{
			{ID: 1, Title: "Track One", Seconds: 300, Position: 1, Medium: 1},
			{ID: 2, Title: "Track Two", Seconds: 300, Position: 2, Medium: 1},
			{ID: 3, Title: "Track Three", Seconds: 300, Position: 1, Medium: 2},
		},
	}
}

type harness struct {
	stub   *qobuztest.Stub
	client *qobuz.Client
	dir    string
	out    bytes.Buffer
}

func start(t *testing.T, stub *qobuztest.Stub, opts qobuz.Options) *harness {
	t.Helper()
	if opts.AuthToken == "" {
		opts.AuthToken = "token"
	}
	if opts.RetryWait == 0 {
		opts.RetryWait = time.Millisecond
	}
	return &harness{stub: stub, client: stub.Start(t, opts), dir: t.TempDir()}
}

func (h *harness) downloader(t *testing.T, mutate func(*download.Options)) *download.Downloader {
	t.Helper()
	opts := download.Options{Client: h.client, Directory: h.dir, Quality: qobuz.FormatHiRes, Out: &h.out}
	if mutate != nil {
		mutate(&opts)
	}
	d, err := download.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func comments(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	fields := map[string]string{}
	for _, block := range f.Meta {
		if block.Type != flac.VorbisComment {
			continue
		}
		c, err := flacvorbis.ParseFromMetaDataBlock(*block)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range c.Comments {
			k, v, _ := strings.Cut(line, "=")
			fields[k] = v
		}
	}
	return fields
}

func hasPicture(t *testing.T, path string) bool {
	t.Helper()
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range f.Meta {
		if block.Type == flac.Picture {
			return true
		}
	}
	return false
}

// The whole point: an album URL becomes a folder of tagged files with a cover.
func TestAlbumBecomesATaggedFolder(t *testing.T) {
	h := start(t, qobuztest.New(firstAlbum()), qobuz.Options{})
	d := h.downloader(t, func(o *download.Options) { o.EmbedArt = true })

	result, err := d.AlbumByID(context.Background(), "abc123def456g")
	if err != nil {
		t.Fatalf("AlbumByID: %v", err)
	}
	want := filepath.Join(h.dir, "Some Band - First Album (2023) [24B-96kHz]")
	if result.Dir != want || result.Downloaded != 2 || result.Failed != 0 {
		t.Fatalf("result = %+v, want dir %s and 2 downloaded", result, want)
	}

	first := filepath.Join(want, "01. Opening Track.flac")
	for _, name := range []string{first, filepath.Join(want, "02. Second Track.flac"), filepath.Join(want, "cover.jpg")} {
		if _, err := os.Stat(name); err != nil {
			t.Errorf("%s did not arrive: %v", name, err)
		}
	}
	cover, _ := os.ReadFile(filepath.Join(want, "cover.jpg"))
	if len(cover) != len(qobuztest.LargeCoverBytes()) {
		t.Errorf("cover is %d bytes, want the large rendition (%d)", len(cover), len(qobuztest.LargeCoverBytes()))
	}

	fields := comments(t, first)
	for k, v := range map[string]string{
		"TITLE": "Opening Track", "ARTIST": "Some Band", "ALBUM": "First Album", "ALBUMARTIST": "Some Band", "COMPOSER": "A Composer",
		"GENRE": "Metal", "DATE": "2023-03-03", "LABEL": "Some Label", "BARCODE": "0000000000002",
		"TRACKNUMBER": "1", "TRACKTOTAL": "2", "DISCNUMBER": "1", "DISCTOTAL": "1", "ISRC": "GBBKS1700001",
	} {
		if fields[k] != v {
			t.Errorf("%s = %q, want %q", k, fields[k], v)
		}
	}
	if !hasPicture(t, first) {
		t.Error("the cover should be embedded")
	}
	if entries, _ := os.ReadDir(want); len(entries) != 3 {
		t.Errorf("folder holds %d entries, want 3 (no .part, no .m3u)", len(entries))
	}
}

// A second run does nothing: the history says the album is done, and the files that are
// already on disk are not fetched again when the history is off.
func TestAlbumIsNotDownloadedTwice(t *testing.T) {
	h := start(t, qobuztest.New(firstAlbum()), qobuz.Options{})
	store, err := history.Open(filepath.Join(t.TempDir(), "downloaded.txt"))
	if err != nil {
		t.Fatal(err)
	}
	d := h.downloader(t, func(o *download.Options) { o.History = store })

	if _, err := d.AlbumByID(context.Background(), "abc123def456g"); err != nil {
		t.Fatal(err)
	}
	if !store.Has("album", "abc123def456g") {
		t.Fatal("a complete album goes into the history")
	}
	result, err := d.AlbumByID(context.Background(), "abc123def456g")
	if err != nil || result.Skipped != 2 || result.Downloaded != 0 {
		t.Errorf("second run = %+v, %v; want everything skipped", result, err)
	}

	// Without the history, the files on disk are what stops a second fetch.
	plain := h.downloader(t, nil)
	result, err = plain.AlbumByID(context.Background(), "abc123def456g")
	if err != nil || result.Skipped != 2 {
		t.Errorf("run without history = %+v, %v; want files on disk skipped", result, err)
	}
	if got := h.stub.Count("/stream/900000001.flac"); got != 1 {
		t.Errorf("the stream was fetched %d times, want 1", got)
	}
}

func TestMultiDiscAlbumGetsDiscFolders(t *testing.T) {
	h := start(t, qobuztest.New(boxSet()), qobuz.Options{})
	d := h.downloader(t, func(o *download.Options) { o.AlbumM3U = true; o.NoCover = true })

	result, err := d.AlbumByID(context.Background(), "box")
	if err != nil {
		t.Fatal(err)
	}
	// The slash in the artist's name is not a path separator, and the version joins the title.
	want := filepath.Join(h.dir, "Slash-Band - Live Set (Deluxe Edition) (1992) [24B-96kHz]")
	if result.Dir != want {
		t.Fatalf("dir = %s, want %s", result.Dir, want)
	}
	for _, name := range []string{"Disc 1/01. Track One.flac", "Disc 1/02. Track Two.flac", "Disc 2/01. Track Three.flac"} {
		if _, err := os.Stat(filepath.Join(want, name)); err != nil {
			t.Errorf("%s did not arrive: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(want, "cover.jpg")); !os.IsNotExist(err) {
		t.Error("NoCover should leave no cover.jpg")
	}
	fields := comments(t, filepath.Join(want, "Disc 2/01. Track Three.flac"))
	if fields["DISCNUMBER"] != "2" || fields["DISCTOTAL"] != "2" || fields["TRACKTOTAL"] != "1" {
		t.Errorf("disc 2 tags = %v", fields)
	}
	m3u, err := os.ReadFile(filepath.Join(want, filepath.Base(want)+".m3u"))
	if err != nil {
		t.Fatalf("m3u: %v", err)
	}
	if !strings.Contains(string(m3u), "Disc 1/01. Track One.flac\n") || !strings.HasPrefix(string(m3u), "#EXTM3U\n") {
		t.Errorf("m3u = %q", m3u)
	}
}

// The folder says what the files carry, not what was asked for: a 16-bit master asked for
// in hi-res is a 16-bit folder, and MP3 has no bit depth to speak of.
func TestFolderNamesTheDeliveredQuality(t *testing.T) {
	cd := firstAlbum()
	cd.MaxBitDepth, cd.MaxSampleRate, cd.NoHiRes = 16, 44.1, true
	h := start(t, qobuztest.New(cd), qobuz.Options{})

	if _, err := h.downloader(t, nil).AlbumByID(context.Background(), cd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "Some Band - First Album (2023) [16B-44.1kHz]")); err != nil {
		t.Error("a 16-bit master should be named as one")
	}

	mp3 := h.downloader(t, func(o *download.Options) { o.Quality = qobuz.FormatMP3 })
	if _, err := mp3.AlbumByID(context.Background(), cd.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "Some Band - First Album (2023) [MP3-320]", "01. Opening Track.mp3")); err != nil {
		t.Error("an MP3 download should be named as one and carry .mp3")
	}
}

func TestOneFailingTrackDoesNotAbandonTheAlbum(t *testing.T) {
	stub := qobuztest.New(firstAlbum())
	stub.File = func(query url.Values) (int, map[string]any) {
		if query.Get("track_id") == "900000001" {
			return 200, map[string]any{"format_id": 27} // no url: the account may not stream it
		}
		return 200, map[string]any{
			"url": stub.BaseURL + "/stream/" + query.Get("track_id") + ".flac", "format_id": 27,
			"bit_depth": 24, "sampling_rate": 96,
		}
	}
	h := start(t, stub, qobuz.Options{})
	store, _ := history.Open(filepath.Join(t.TempDir(), "h.txt"))
	d := h.downloader(t, func(o *download.Options) { o.History = store })

	result, err := d.AlbumByID(context.Background(), "abc123def456g")
	if err == nil || !errors.Is(err, qobuz.ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable for the missing track", err)
	}
	if result.Downloaded != 1 || result.Failed != 1 {
		t.Errorf("result = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(result.Dir, "02. Second Track.flac")); err != nil {
		t.Error("the other track should still have arrived")
	}
	if store.Has("album", "abc123def456g") {
		t.Error("an incomplete album must not go into the history, so a later run finishes it")
	}
	if !strings.Contains(h.out.String(), "failed") {
		t.Errorf("output should say what failed:\n%s", h.out.String())
	}
}

func TestATrackIsFetchedAgainAfterAServerError(t *testing.T) {
	stub := qobuztest.New(firstAlbum())
	stub.StreamFailures = 2
	h := start(t, stub, qobuz.Options{Attempts: 3})

	if _, err := h.downloader(t, nil).AlbumByID(context.Background(), "abc123def456g"); err != nil {
		t.Fatalf("AlbumByID: %v", err)
	}
	if got := stub.Count("/stream/900000001.flac"); got != 3 {
		t.Errorf("the first stream was asked for %d times, want 3", got)
	}
}

func TestSingleTrackGoesIntoItsAlbumFolder(t *testing.T) {
	h := start(t, qobuztest.New(firstAlbum()), qobuz.Options{})
	d := h.downloader(t, nil)

	result, err := d.TrackByID(context.Background(), "900000002")
	if err != nil {
		t.Fatalf("TrackByID: %v", err)
	}
	path := filepath.Join(h.dir, "Some Band - First Album (2023) [24B-96kHz]", "02. Second Track.flac")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s did not arrive: %v", path, err)
	}
	if result.Downloaded != 1 {
		t.Errorf("result = %+v", result)
	}
	if fields := comments(t, path); fields["TRACKNUMBER"] != "2" || fields["TRACKTOTAL"] != "2" {
		t.Errorf("tags = %v", fields)
	}
}

func TestPlaylistIsOneFlatFolderWithAnM3U(t *testing.T) {
	stub := qobuztest.New(firstAlbum(), boxSet())
	stub.Playlists["77"] = qobuztest.Playlist{ID: "77", Name: "Mix: Essentials", Owner: "Qobuz",
		Tracks: [][2]string{{"box", "3"}, {"abc123def456g", "900000001"}}}
	h := start(t, stub, qobuz.Options{})
	d := h.downloader(t, func(o *download.Options) { o.EmbedArt = true })

	result, err := d.PlaylistByID(context.Background(), "77")
	if err != nil {
		t.Fatalf("PlaylistByID: %v", err)
	}
	dir := filepath.Join(h.dir, "Mix- Essentials")
	if result.Dir != dir || result.Downloaded != 2 {
		t.Fatalf("result = %+v", result)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"01. Track Three.flac", "02. Opening Track.flac", "Mix- Essentials.m3u"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("folder = %v, want %v", names, want)
	}
	fields := comments(t, filepath.Join(dir, "01. Track Three.flac"))
	if fields["ALBUM"] != "Live Set (Deluxe Edition)" || fields["TRACKNUMBER"] != "1" || fields["DISCNUMBER"] != "2" {
		t.Errorf("a playlist track keeps its album's tags: %v", fields)
	}
	if !hasPicture(t, filepath.Join(dir, "02. Opening Track.flac")) {
		t.Error("EmbedArt should embed each track's own cover")
	}
}

func TestArtistDiscographyCanBeAlbumsOnly(t *testing.T) {
	ep := firstAlbum()
	ep.ID, ep.Title, ep.ReleaseType = "ep", "Short Release", "epmini"
	single := firstAlbum()
	single.ID, single.Title, single.ReleaseType = "single", "Second Track (Single)", ""
	various := firstAlbum()
	various.ID, various.Title, various.Artist, various.ReleaseType = "va", "A Compilation", "Various Artists", "album"
	stub := qobuztest.New(firstAlbum(), ep, single, various)
	stub.ArtistName = "Some Band"
	stub.ArtistAlbums = []string{"abc123def456g", "ep", "single", "va"}
	h := start(t, stub, qobuz.Options{})

	d := h.downloader(t, func(o *download.Options) { o.AlbumsOnly = true })
	result, err := d.ArtistByID(context.Background(), "50505")
	if err != nil {
		t.Fatalf("ArtistByID: %v", err)
	}
	if result.Downloaded != 2 {
		t.Errorf("downloaded %d tracks, want the 2 of the one real album", result.Downloaded)
	}
	entries, _ := os.ReadDir(h.dir)
	if len(entries) != 1 || entries[0].Name() != "Some Band - First Album (2023) [24B-96kHz]" {
		t.Errorf("folders = %v", entries)
	}

	everything := h.downloader(t, nil)
	if result, err := everything.ArtistByID(context.Background(), "50505"); err != nil || result.Downloaded != 6 {
		t.Errorf("without AlbumsOnly: %+v, %v; want the 3 other releases too", result, err)
	}
}

func TestIsAlbum(t *testing.T) {
	for _, tc := range []struct {
		album qobuz.Album
		want  bool
	}{
		{qobuz.Album{Title: "First Album", Artist: "Some Band", ReleaseType: "album"}, true},
		{qobuz.Album{Title: "First Album", Artist: "Some Band"}, true},
		{qobuz.Album{Title: "Some Release - EP", Artist: "Some Band"}, false},
		{qobuz.Album{Title: "Short Release", Artist: "Some Band", ReleaseType: "epmini"}, false},
		{qobuz.Album{Title: "A Single", Artist: "Some Band", ReleaseType: "single"}, false},
		{qobuz.Album{Title: "A Compilation", Artist: "Various Artists", ReleaseType: "album"}, false},
		{qobuz.Album{Title: "Split Release", Artist: "Someone Else", ReleaseType: "album"}, false},
	} {
		if got := download.IsAlbum(&tc.album, "Some Band"); got != tc.want {
			t.Errorf("IsAlbum(%q by %s, %q) = %v, want %v", tc.album.Title, tc.album.Artist, tc.album.ReleaseType, got, tc.want)
		}
	}
}

func TestCustomTemplates(t *testing.T) {
	h := start(t, qobuztest.New(firstAlbum()), qobuz.Options{})
	d := h.downloader(t, func(o *download.Options) {
		o.FolderFormat = "{year} {album} [{quality}]"
		o.TrackFormat = "{discnumber}-{tracknumber} {trackartist} - {tracktitle}"
	})
	if _, err := d.AlbumByID(context.Background(), "abc123def456g"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "2023 First Album [flac-24-96]", "1-01 Some Band - Opening Track.flac")); err != nil {
		t.Error(err)
	}
}
