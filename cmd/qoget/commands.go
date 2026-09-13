package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/davidetoniatti/qoget/internal/download"
	"github.com/davidetoniatti/qoget/internal/link"
	"github.com/davidetoniatti/qoget/internal/qobuz"
)

// download is "qoget dl": every argument is a Qobuz URL, a <kind>:<id>, or a text file of
// them, one per line.
func (env *environment) download(ctx context.Context, args []string) error {
	fs, f, err := env.newFlagSet("dl", "dl [flags] <url|kind:id|file.txt>...")
	if err != nil {
		return err
	}
	var listFile string
	fs.StringVar(&listFile, "i", "", "read URLs from this file, one per line")
	if err := fs.Parse(args); err != nil {
		return err
	}

	targets := fs.Args()
	if listFile != "" {
		targets = append(targets, listFile)
	}
	if len(targets) == 0 {
		fs.Usage()
		return errors.New("nothing to download")
	}
	links, err := resolveTargets(targets)
	if err != nil {
		return err
	}

	s, err := env.session(f)
	if err != nil {
		return err
	}
	var total download.Result
	var failures []error
	for _, l := range links {
		result, err := s.fetch(ctx, l)
		total.Downloaded += result.Downloaded
		total.Skipped += result.Skipped
		total.Failed += result.Failed
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", l.Kind, l.ID, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return report(env.stdout, total, errors.Join(failures...))
}

// fetch downloads whatever one link names.
func (s *session) fetch(ctx context.Context, l link.Link) (download.Result, error) {
	switch l.Kind {
	case link.Album:
		return s.downloader.AlbumByID(ctx, l.ID)
	case link.Track:
		return s.downloader.TrackByID(ctx, l.ID)
	case link.Playlist:
		return s.downloader.PlaylistByID(ctx, l.ID)
	case link.Artist:
		return s.downloader.ArtistByID(ctx, l.ID)
	case link.Label:
		return s.downloader.LabelByID(ctx, l.ID)
	}
	return download.Result{}, fmt.Errorf("unsupported link %+v", l)
}

// resolveTargets parses every argument; a readable file that is not a URL contributes its
// lines. The whole list is parsed before anything downloads, so a typo is reported at once
// rather than after an hour.
func resolveTargets(targets []string) ([]link.Link, error) {
	var links []link.Link
	for _, target := range targets {
		if l, err := link.Parse(target); err == nil {
			links = append(links, l)
			continue
		}
		data, readErr := os.ReadFile(target)
		if readErr != nil {
			_, parseErr := link.Parse(target)
			return nil, fmt.Errorf("%q: %w", target, parseErr)
		}
		for n, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			l, err := link.Parse(line)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %w", target, n+1, err)
			}
			links = append(links, l)
		}
	}
	if len(links) == 0 {
		return nil, errors.New("nothing to download")
	}
	return links, nil
}

// info is "qoget info": what a URL points at, without downloading.
func (env *environment) info(ctx context.Context, args []string) error {
	fs, f, err := env.newFlagSet("info", "info <url|kind:id>")
	if err != nil {
		return err
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return errors.New("info takes one URL")
	}
	l, err := link.Parse(fs.Arg(0))
	if err != nil {
		return err
	}
	s, err := env.session(f)
	if err != nil {
		return err
	}
	w := env.stdout
	switch l.Kind {
	case link.Album:
		album, err := s.client.Album(ctx, l.ID)
		if err != nil {
			return err
		}
		printAlbum(w, album)
	case link.Track:
		track, err := s.client.Track(ctx, l.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "track    %s\n", track.FullTitle())
		fmt.Fprintf(w, "by       %s\n", orText(track.Performer, track.Album.Artist))
		fmt.Fprintf(w, "album    %s - %s (%s)\n", track.Album.Artist, track.Album.FullTitle(), track.Album.Year())
		fmt.Fprintf(w, "length   %s   isrc %s   %s\n", clock(track.Duration), track.ISRC, streamable(track.Streamable))
	case link.Artist:
		artist, err := s.client.Artist(ctx, l.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "artist   %s (%d releases)\n\n", artist.Name, len(artist.Albums))
		printAlbumList(w, artist.Albums)
	case link.Label:
		label, err := s.client.Label(ctx, l.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "label    %s (%d releases)\n\n", label.Name, len(label.Albums))
		printAlbumList(w, label.Albums)
	case link.Playlist:
		playlist, err := s.client.Playlist(ctx, l.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "playlist %s by %s (%d tracks)\n\n", playlist.Name, playlist.Owner, len(playlist.Tracks))
		for i, track := range playlist.Tracks {
			album := ""
			if track.Album != nil {
				album = track.Album.Artist + " - " + track.Album.FullTitle()
			}
			fmt.Fprintf(w, "%3d. %-40s %s  %s\n", i+1, cut(track.FullTitle(), 40), clock(track.Duration), album)
		}
	}
	return nil
}

func printAlbum(w io.Writer, album *qobuz.Album) {
	fmt.Fprintf(w, "album    %s\n", album.FullTitle())
	fmt.Fprintf(w, "artist   %s\n", album.Artist)
	fmt.Fprintf(w, "released %s   label %s   genre %s\n", album.Date, album.Label, album.Genre)
	fmt.Fprintf(w, "quality  %d-bit/%skHz   hi-res %s   %s\n", album.MaxBitDepth, strconv.FormatFloat(album.MaxSampleRate, 'f', -1, 64), yesNo(album.HiResStreamable), streamable(album.Streamable))
	fmt.Fprintf(w, "id       %s   upc %s   %d tracks on %d disc(s)\n\n", album.ID, album.UPC, album.TrackCount, max(album.MediaCount, 1))
	for _, track := range album.Tracks {
		flag := ""
		if !track.Streamable {
			flag = "  (not streamable)"
		}
		fmt.Fprintf(w, "%d-%02d  %-50s %s%s\n", track.Medium, track.Position, cut(track.FullTitle(), 50), clock(track.Duration), flag)
	}
}

func printAlbumList(w io.Writer, albums []qobuz.Album) {
	for i, album := range albums {
		fmt.Fprintf(w, "%3d. %s - %s (%s)  %d tracks  %d-bit/%skHz  %s  id=%s\n", i+1, album.Artist, album.FullTitle(), album.Year(),
			album.TrackCount, album.MaxBitDepth, strconv.FormatFloat(album.MaxSampleRate, 'f', -1, 64), streamable(album.Streamable), album.ID)
	}
}

// search is "qoget search": list what Qobuz finds, let the user pick.
func (env *environment) search(ctx context.Context, args []string) error {
	fs, f, err := env.newFlagSet("search", "search [flags] <query>")
	if err != nil {
		return err
	}
	var kind string
	var limit int
	fs.StringVar(&kind, "t", "album", "what to search for: album, track, artist or playlist")
	fs.IntVar(&limit, "l", f.cfg.Limit, "how many results to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		fs.Usage()
		return errors.New("search needs a query")
	}
	s, err := env.session(f)
	if err != nil {
		return err
	}

	links, err := s.find(ctx, env.stdout, kind, query, limit)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		fmt.Fprintln(env.stdout, "nothing found")
		return nil
	}

	fmt.Fprintf(env.stdout, "\nPick what to download (e.g. 1,3-5 or all; q to quit): ")
	line, err := bufio.NewReader(env.stdin).ReadString('\n')
	if err != nil && line == "" {
		return nil
	}
	chosen, err := selection(strings.TrimSpace(line), len(links))
	if err != nil {
		return err
	}
	if len(chosen) == 0 {
		return nil
	}
	fmt.Fprintln(env.stdout)
	return s.fetchAll(ctx, env.stdout, links, chosen)
}

// lucky is "qoget lucky": the first results of a search, downloaded without asking.
func (env *environment) lucky(ctx context.Context, args []string) error {
	fs, f, err := env.newFlagSet("lucky", "lucky [flags] <query>")
	if err != nil {
		return err
	}
	var kind string
	var count int
	fs.StringVar(&kind, "t", "album", "what to search for: album, track, artist or playlist")
	fs.IntVar(&count, "n", 1, "how many results to download")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		fs.Usage()
		return errors.New("lucky needs a query")
	}
	s, err := env.session(f)
	if err != nil {
		return err
	}
	links, err := s.find(ctx, env.stdout, kind, query, count)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return errors.New("nothing found")
	}
	fmt.Fprintln(env.stdout)
	all := make([]int, len(links))
	for i := range all {
		all[i] = i
	}
	return s.fetchAll(ctx, env.stdout, links, all)
}

// find runs one search and prints the results numbered, returning them as links.
func (s *session) find(ctx context.Context, w io.Writer, kind, query string, limit int) ([]link.Link, error) {
	var links []link.Link
	switch strings.ToLower(kind) {
	case "album", "albums":
		albums, err := s.client.SearchAlbums(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		for i, album := range albums {
			fmt.Fprintf(w, "%3d. %s - %s (%s)  %d tracks  %d-bit/%skHz  %s\n", i+1, album.Artist, album.FullTitle(), album.Year(),
				album.TrackCount, album.MaxBitDepth, strconv.FormatFloat(album.MaxSampleRate, 'f', -1, 64), streamable(album.Streamable))
			links = append(links, link.Link{Kind: link.Album, ID: album.ID})
		}
	case "track", "tracks":
		tracks, err := s.client.SearchTracks(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		for i, track := range tracks {
			album := ""
			if track.Album != nil {
				album = fmt.Sprintf("  [%s - %s (%s)]", track.Album.Artist, track.Album.FullTitle(), track.Album.Year())
			}
			fmt.Fprintf(w, "%3d. %s - %s  %s%s\n", i+1, orText(track.Performer, ""), track.FullTitle(), clock(track.Duration), album)
			links = append(links, link.Link{Kind: link.Track, ID: track.ID})
		}
	case "artist", "artists":
		artists, err := s.client.SearchArtists(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		for i, artist := range artists {
			fmt.Fprintf(w, "%3d. %s  (id %s)\n", i+1, artist.Name, artist.ID)
			links = append(links, link.Link{Kind: link.Artist, ID: artist.ID})
		}
	case "playlist", "playlists":
		playlists, err := s.client.SearchPlaylists(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		for i, playlist := range playlists {
			fmt.Fprintf(w, "%3d. %s  by %s\n", i+1, playlist.Name, playlist.Owner)
			links = append(links, link.Link{Kind: link.Playlist, ID: playlist.ID})
		}
	default:
		return nil, fmt.Errorf("unknown type %q: album, track, artist or playlist", kind)
	}
	return links, nil
}

func (s *session) fetchAll(ctx context.Context, w io.Writer, links []link.Link, chosen []int) error {
	var total download.Result
	var failures []error
	for _, i := range chosen {
		result, err := s.fetch(ctx, links[i])
		total.Downloaded += result.Downloaded
		total.Skipped += result.Skipped
		total.Failed += result.Failed
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", links[i].Kind, links[i].ID, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return report(w, total, errors.Join(failures...))
}

// selection reads "1,3-5", "all" or "q" into zero-based indexes.
func selection(input string, count int) ([]int, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	switch input {
	case "", "q", "quit", "n", "none":
		return nil, nil
	case "all", "a", "*":
		all := make([]int, count)
		for i := range all {
			all[i] = i
		}
		return all, nil
	}
	seen := map[int]bool{}
	var chosen []int
	for _, part := range strings.FieldsFunc(input, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		from, to := part, part
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, to = a, b
		}
		lo, err1 := strconv.Atoi(from)
		hi, err2 := strconv.Atoi(to)
		if err1 != nil || err2 != nil || lo < 1 || hi > count || lo > hi {
			return nil, fmt.Errorf("%q is not a choice between 1 and %d", part, count)
		}
		for i := lo; i <= hi; i++ {
			if !seen[i] {
				seen[i] = true
				chosen = append(chosen, i-1)
			}
		}
	}
	return chosen, nil
}

func clock(d time.Duration) string {
	d = d.Round(time.Second)
	if d >= time.Hour {
		return fmt.Sprintf("%d:%02d:%02d", int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60)
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func streamable(ok bool) string {
	if ok {
		return "streamable"
	}
	return "NOT streamable"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func orText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
