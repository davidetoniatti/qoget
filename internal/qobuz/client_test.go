package qobuz_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davidetoniatti/qoget/internal/qobuz"
	"github.com/davidetoniatti/qoget/internal/qobuz/qobuztest"
)

func exampleAlbum() qobuztest.Album {
	return qobuztest.Album{
		ID:     "0000000000001",
		Title:  "Example Album",
		Artist: "Example Artist",
		Label:  "Example Label",
		Date:   "1997-05-21",
		Tracks: []qobuztest.Track{
			{ID: 10000001, Title: "First Song", Seconds: 284, Position: 1, Medium: 1},
			{ID: 10000002, Title: "Second Song", Seconds: 383, Position: 2, Medium: 1},
		},
	}
}

// The app id lives in the player bundle, so the first request pays for two more. Every
// request after it must not.
func TestSearchReadsThePlayerBundleOnce(t *testing.T) {
	stub := qobuztest.New(exampleAlbum())
	client := stub.Start(t, qobuz.Options{})
	ctx := context.Background()

	for range 3 {
		albums, err := client.SearchAlbums(ctx, "Example Artist Example Album", 5)
		if err != nil {
			t.Fatalf("SearchAlbums: %v", err)
		}
		if len(albums) != 1 {
			t.Fatalf("albums = %+v", albums)
		}
		album := albums[0]
		if album.ID != "0000000000001" || album.Title != "Example Album" || album.Artist != "Example Artist" {
			t.Errorf("album = %+v", album)
		}
		if album.TrackCount != 2 || !album.Streamable || album.MaxBitDepth != 24 {
			t.Errorf("album = %+v", album)
		}
		if len(album.Tracks) != 0 {
			t.Error("a search result carries no track listing")
		}
	}

	if got := stub.Count("/login"); got != 1 {
		t.Errorf("login fetched %d times, want 1", got)
	}
	if got := stub.Count("/resources/8.2.0/bundle.js"); got != 1 {
		t.Errorf("bundle fetched %d times, want 1", got)
	}
}

// An album id comes back quoted in some responses and bare in others.
func TestSearchAcceptsAnUnquotedAlbumID(t *testing.T) {
	album := exampleAlbum()
	album.ID, album.NumericID = "10000000", true

	stub := qobuztest.New(album)
	client := stub.Start(t, qobuz.Options{AppID: "798273057", Secrets: []string{"s"}})

	albums, err := client.SearchAlbums(context.Background(), "Example Artist Example Album", 5)
	if err != nil {
		t.Fatalf("SearchAlbums: %v", err)
	}
	if len(albums) != 1 || albums[0].ID != "10000000" {
		t.Fatalf("albums = %+v", albums)
	}
}

func TestAlbumReadsTheWholeListing(t *testing.T) {
	stub := qobuztest.New(exampleAlbum())
	client := stub.Start(t, qobuz.Options{AppID: "798273057", Secrets: []string{"s"}})

	album, err := client.Album(context.Background(), "0000000000001")
	if err != nil {
		t.Fatalf("Album: %v", err)
	}
	if len(album.Tracks) != 2 {
		t.Fatalf("tracks = %+v", album.Tracks)
	}
	if album.Tracks[0].ID != "10000001" || album.Tracks[0].Title != "First Song" {
		t.Errorf("first track = %+v", album.Tracks[0])
	}
	if album.Tracks[0].Duration.Seconds() != 284 {
		t.Errorf("duration = %v, want 284s", album.Tracks[0].Duration)
	}
	if !album.Tracks[0].Streamable {
		t.Error("track should be streamable")
	}
}

// A listing that is missing or truncated must be refused rather than scored: the track
// count is the strongest evidence a candidate has.
func TestAlbumRefusesAnIncompleteListing(t *testing.T) {
	t.Run("no listing at all", func(t *testing.T) {
		album := exampleAlbum()
		album.OmitListing = true
		client := qobuztest.New(album).Start(t, qobuz.Options{AppID: "1", Secrets: []string{"s"}})

		if _, err := client.Album(context.Background(), album.ID); err == nil ||
			!strings.Contains(err.Error(), "track listing") {
			t.Fatalf("Album error = %v", err)
		}
	})

	t.Run("truncated listing", func(t *testing.T) {
		album := exampleAlbum()
		album.ClaimedTotal = 12
		client := qobuztest.New(album).Start(t, qobuz.Options{AppID: "1", Secrets: []string{"s"}})

		if _, err := client.Album(context.Background(), album.ID); err == nil ||
			!strings.Contains(err.Error(), "truncated") {
			t.Fatalf("Album error = %v", err)
		}
	})
}

func TestAlbumReportsWhatTheApiSaid(t *testing.T) {
	client := qobuztest.New(exampleAlbum()).Start(t, qobuz.Options{AppID: "1", Secrets: []string{"s"}})

	_, err := client.Album(context.Background(), "does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "Album not found") {
		t.Fatalf("Album error = %v, want the message Qobuz gave", err)
	}
}

// Secrets are tried in turn and the winner is remembered, so a run of tracks pays for the
// wrong secret once rather than once each.
func TestFileURLTriesEverySecretAndRemembersTheWinner(t *testing.T) {
	stub := qobuztest.New(exampleAlbum())
	stub.Secrets = []string{"b3a1f0e9c2d4a5b6c7d8e9f0a1b2c3d4", "5f4e3d2c1b0a9f8e7d6c5b4a39281706"}
	// The player prefers the second seed, so the client tries it first. Making the first
	// one the valid one is what forces it through both.
	stub.ValidSecret = stub.Secrets[0]

	client := stub.Start(t, qobuz.Options{AuthToken: "token"})
	ctx := context.Background()

	file, err := client.FileURL(ctx, "10000001", qobuz.FormatHiRes)
	if err != nil {
		t.Fatalf("FileURL: %v", err)
	}
	if file.Format != qobuz.FormatHiRes || !strings.HasSuffix(file.URL, "10000001.flac") {
		t.Errorf("file = %+v", file)
	}
	if got := stub.Count("/api.json/0.2/track/getFileUrl"); got != 2 {
		t.Fatalf("getFileUrl called %d times, want 2 (one refused signature)", got)
	}

	if _, err := client.FileURL(ctx, "10000002", qobuz.FormatHiRes); err != nil {
		t.Fatalf("second FileURL: %v", err)
	}
	if got := stub.Count("/api.json/0.2/track/getFileUrl"); got != 3 {
		t.Errorf("getFileUrl called %d times, want 3: the working secret should be remembered", got)
	}
}

func TestFileURLRefusesWhatItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		file    func(url.Values) (int, map[string]any)
		wantErr error
		message string
	}{
		{
			name: "a preview instead of the track",
			file: func(query url.Values) (int, map[string]any) {
				return 200, map[string]any{
					"url":       "https://streaming.qobuz.example/sample.mp3",
					"format_id": 27, "sample": true,
				}
			},
			wantErr: qobuz.ErrUnavailable,
		},
		{
			name: "no url at all",
			file: func(query url.Values) (int, map[string]any) {
				return 200, map[string]any{"format_id": 27}
			},
			wantErr: qobuz.ErrUnavailable,
		},
		{
			name: "another container than the manifest names",
			file: func(query url.Values) (int, map[string]any) {
				return 200, map[string]any{
					"url":       "https://streaming.qobuz.example/10000001.mp3",
					"format_id": int(qobuz.FormatMP3),
				}
			},
			message: "came back as mp3-320",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := qobuztest.New(exampleAlbum())
			stub.File = tc.file
			client := stub.Start(t, qobuz.Options{AuthToken: "token"})

			_, err := client.FileURL(context.Background(), "10000001", qobuz.FormatHiRes)
			if err == nil {
				t.Fatal("FileURL should have failed")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("FileURL error = %v, want %v", err, tc.wantErr)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("FileURL error = %v, want it to mention %q", err, tc.message)
			}
		})
	}
}

// Searching needs no account; a stream URL does. Failing before the request is what makes
// the difference visible in the logs.
func TestFileURLWithoutAnAccountToken(t *testing.T) {
	stub := qobuztest.New(exampleAlbum())
	client := stub.Start(t, qobuz.Options{})

	_, err := client.FileURL(context.Background(), "10000001", qobuz.FormatHiRes)
	if !errors.Is(err, qobuz.ErrNoAuthToken) {
		t.Fatalf("FileURL error = %v, want ErrNoAuthToken", err)
	}
	if got := stub.Count("/api.json/0.2/track/getFileUrl"); got != 0 {
		t.Errorf("getFileUrl called %d times without a token", got)
	}

	if _, err := client.SearchAlbums(context.Background(), "Example Artist Example Album", 5); err != nil {
		t.Errorf("searching should work without a token: %v", err)
	}
}

// A file appears under the name the manifest gave it when it is whole, and not before.
func TestDownloadLeavesNothingHalfWritten(t *testing.T) {
	audio := []byte("fLaC" + strings.Repeat("audio", 200))

	t.Run("whole file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "sub", "01 - First Song.flac")

		// What the destination looked like while the transfer was still running. The name in
		// the manifest must not exist yet, however far along the transfer is.
		var appearedAs string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
			_, _ = w.Write(audio[:1])
			w.(http.Flusher).Flush()

			// Wait for the transfer to have opened something, rather than guessing how long
			// that takes.
			for range 200 {
				if _, err := os.Stat(path); err == nil {
					appearedAs = "the manifest's name"
					break
				}
				if _, err := os.Stat(path + ".part"); err == nil {
					appearedAs = "a temporary name"
					break
				}
				time.Sleep(time.Millisecond)
			}
			_, _ = w.Write(audio[1:])
		}))
		t.Cleanup(server.Close)

		client := qobuz.New(qobuz.Options{})
		written, err := client.Download(context.Background(), &qobuz.File{URL: server.URL}, path, nil)
		if err != nil {
			t.Fatalf("Download: %v", err)
		}
		if written != int64(len(audio)) {
			t.Errorf("wrote %d bytes, want %d", written, len(audio))
		}
		if appearedAs != "a temporary name" {
			t.Errorf("mid-transfer the file was at %q, want a temporary name", appearedAs)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(audio) {
			t.Fatalf("file = %q, %v", got, err)
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Error("the temporary file should be gone")
		}
	})

	t.Run("transfer cut short", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Promising more than is sent is what a dropped transfer looks like.
			w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
			_, _ = w.Write(audio[:64])
		}))
		t.Cleanup(server.Close)

		client := qobuz.New(qobuz.Options{})
		path := filepath.Join(t.TempDir(), "01 - First Song.flac")

		if _, err := client.Download(context.Background(), &qobuz.File{URL: server.URL}, path, nil); err == nil {
			t.Fatal("Download should have failed")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Error("a half-written file must not appear under the manifest's name")
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Error("the temporary file should have been removed")
		}
	})
}

// The API timeout bounds a whole exchange, so sharing it with a download would abort a long
// file part-way through. Getting the stream started is bounded; reading it is not.
func TestStreamIsNotBoundByTheApiTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow/album/get" {
			time.Sleep(400 * time.Millisecond)
			qobuztest.WriteJSON(w, http.StatusOK, map[string]any{"id": "1"})
			return
		}

		// Trickles for longer than an API call is allowed to take in total.
		w.Header().Set("Content-Length", "4")
		for _, b := range []byte("fLaC") {
			_, _ = w.Write([]byte{b})
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}))
	t.Cleanup(server.Close)

	client := qobuz.New(qobuz.Options{
		APIURL:  server.URL + "/slow",
		AppID:   "798273057",
		Secrets: []string{"s"},
		Timeout: 100 * time.Millisecond,
	})
	ctx := context.Background()

	if _, err := client.Album(ctx, "1"); err == nil {
		t.Error("an API call past its timeout should fail")
	}

	path := filepath.Join(t.TempDir(), "airbag.flac")
	if _, err := client.Download(ctx, &qobuz.File{URL: server.URL + "/audio"}, path, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if string(body) != "fLaC" {
		t.Errorf("download = %q, want the whole body", body)
	}
}

// A started stream that goes quiet is bounded by nothing else: the API timeout is deliberately
// not on it, and the caller's context outlives a whole album.
func TestAStalledStreamIsGivenUpOn(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A response that arrived and a body that never finishes, which is what a wedged
		// CDN edge looks like from here.
		w.Header().Set("Content-Length", "4096")
		_, _ = w.Write([]byte("f"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })

	client := qobuz.New(qobuz.Options{StallTimeout: 50 * time.Millisecond})
	path := filepath.Join(t.TempDir(), "01 - First Song.flac")

	done := make(chan error, 1)
	go func() {
		_, err := client.Download(context.Background(), &qobuz.File{URL: server.URL}, path, nil)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, qobuz.ErrStalled) {
			t.Fatalf("Download error = %v, want ErrStalled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Download did not give up on a stalled stream")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a stalled transfer must not appear under the manifest's name")
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Error("the temporary file should have been removed")
	}
}

// The bound is on the gap between two reads and not on the transfer: a big file over a poor
// connection arrives slowly and must survive.
func TestASlowStreamIsNotAStalledOne(t *testing.T) {
	audio := []byte("fLaC" + strings.Repeat("audio", 20))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(audio)))
		for _, b := range audio {
			_, _ = w.Write([]byte{b})
			w.(http.Flusher).Flush()
			time.Sleep(2 * time.Millisecond)
		}
	}))
	t.Cleanup(server.Close)

	// The whole body takes longer to arrive than the bound; no gap in it comes close.
	client := qobuz.New(qobuz.Options{StallTimeout: 200 * time.Millisecond})
	path := filepath.Join(t.TempDir(), "01 - First Song.flac")

	written, err := client.Download(context.Background(), &qobuz.File{URL: server.URL}, path, nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if written != int64(len(audio)) {
		t.Errorf("wrote %d bytes, want %d", written, len(audio))
	}
}

// The stall clock runs only while Read is in flight; delays between Read calls (such as slow
// disk writes) do not trigger a stall.
func TestStallReaderPausesClockBetweenReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(20 * time.Millisecond):
			_, _ = w.Write([]byte("second"))
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	limit := 50 * time.Millisecond
	reader := qobuz.NewStallReaderForTest(resp.Body, cancel, limit)
	defer func() { _ = reader.Close() }()

	buf := make([]byte, 5)
	n, err := reader.Read(buf)
	if err != nil || n != 5 {
		t.Fatalf("first Read: n=%d err=%v", n, err)
	}

	// Sleep longer than the limit between reads (simulating a slow disk write).
	time.Sleep(100 * time.Millisecond)

	buf = make([]byte, 10)
	n, err = reader.Read(buf)
	if (err != nil && !errors.Is(err, io.EOF)) || n != 6 {
		t.Fatalf("second Read after pause: n=%d err=%v", n, err)
	}
}

// A stall that fires during a read where bytes were returned must remain sticky so that the
// subsequent read on the cancelled transport reports ErrStalled, not context.Canceled.
func TestStallReaderRemainsStalledAcrossReads(t *testing.T) {
	_, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 20 * time.Millisecond
	stream := &scriptedStream{
		reads: []struct {
			data []byte
			wait time.Duration
			err  error
		}{
			{data: []byte("hello"), wait: 50 * time.Millisecond, err: nil},
			{data: nil, err: context.Canceled},
		},
	}

	reader := qobuz.NewStallReaderForTest(stream, cancel, limit)
	defer func() { _ = reader.Close() }()

	buf := make([]byte, 10)
	n, err := reader.Read(buf)
	if err != nil || n != 5 {
		t.Fatalf("first Read: n=%d err=%v, want n=5 err=nil", n, err)
	}

	_, err = reader.Read(buf)
	if !errors.Is(err, qobuz.ErrStalled) {
		t.Fatalf("second Read error = %v, want ErrStalled", err)
	}
}

type scriptedStream struct {
	reads []struct {
		data []byte
		wait time.Duration
		err  error
	}
	index int
}

func (s *scriptedStream) Read(p []byte) (int, error) {
	if s.index >= len(s.reads) {
		return 0, io.EOF
	}
	r := s.reads[s.index]
	s.index++
	if r.wait > 0 {
		time.Sleep(r.wait)
	}
	copy(p, r.data)
	return len(r.data), r.err
}

func (s *scriptedStream) Close() error { return nil }

func TestUnreadablePlayerBundle(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*qobuztest.Stub)
		message string
	}{
		{
			name:    "no bundle in the login page",
			prepare: func(s *qobuztest.Stub) { page := "<html>nothing here</html>"; s.LoginRaw = &page },
			message: "no player bundle",
		},
		{
			name:    "no app id in the bundle",
			prepare: func(s *qobuztest.Stub) { js := "!function(){}();"; s.Bundle = &js },
			message: "no app id",
		},
		{
			name: "no secret seeds in the bundle",
			prepare: func(s *qobuztest.Stub) {
				js := `production:{api:{appId:"798273057",appSecret:"` + strings.Repeat("a", 32) + `"}}`
				s.Bundle = &js
			},
			message: "no secret seeds",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := qobuztest.New(exampleAlbum())
			tc.prepare(stub)
			client := stub.Start(t, qobuz.Options{})

			_, err := client.SearchAlbums(context.Background(), "Example Artist Example Album", 5)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("SearchAlbums error = %v, want it to mention %q", err, tc.message)
			}
		})
	}
}
