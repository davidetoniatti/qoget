// Package download turns what the catalogue describes into files on disk: an album into a
// folder of tagged tracks with its cover, a playlist into a folder with an .m3u, an artist or
// label into one folder per album.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/davidetoniatti/qoget/internal/history"
	"github.com/davidetoniatti/qoget/internal/naming"
	"github.com/davidetoniatti/qoget/internal/qobuz"
	"github.com/davidetoniatti/qoget/internal/tag"
)

// CoverName is the file an album's artwork is saved under, beside the tracks.
const CoverName = "cover.jpg"

// DefaultFolderMP3 replaces the default folder template when MP3 is asked for: the bit
// depth and sampling rate of the master say nothing about a lossy file.
const DefaultFolderMP3 = "{artist} - {album} ({year}) [MP3-320]"

// Options configures a Downloader.
type Options struct {
	Client    *qobuz.Client
	Directory string
	Quality   qobuz.Format

	FolderFormat string
	TrackFormat  string

	// EmbedArt puts the cover into every track's tags; NoCover leaves no cover.jpg beside
	// them. CoverSize is which rendition is fetched.
	EmbedArt  bool
	NoCover   bool
	CoverSize qobuz.CoverSize

	// AlbumM3U writes a playlist file beside every album; NoPlaylistM3U leaves one out of a
	// downloaded playlist, which otherwise always gets one.
	AlbumM3U      bool
	NoPlaylistM3U bool

	// AlbumsOnly leaves singles, EPs and various-artists compilations out of an artist's
	// or a label's discography.
	AlbumsOnly bool

	// History is what has been downloaded already; nil skips nothing.
	History *history.Store

	// Out is where progress is written. Interactive redraws the current line with \r,
	// for a terminal; otherwise every line is final.
	Out         io.Writer
	Interactive bool
}

// Downloader fetches albums, tracks, playlists, artists and labels.
type Downloader struct {
	opts Options
}

// New checks the options and builds a Downloader.
func New(opts Options) (*Downloader, error) {
	if opts.Client == nil {
		return nil, errors.New("download: no client")
	}
	if !opts.Quality.Valid() {
		return nil, fmt.Errorf("download: invalid quality %s", opts.Quality)
	}
	if opts.Directory == "" {
		return nil, errors.New("download: no directory")
	}
	if opts.FolderFormat == "" {
		opts.FolderFormat = naming.DefaultFolder
	}
	if opts.TrackFormat == "" {
		opts.TrackFormat = naming.DefaultTrack
	}
	if opts.CoverSize == "" {
		opts.CoverSize = qobuz.CoverMax
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	return &Downloader{opts: opts}, nil
}

// Result is what one download did.
type Result struct {
	// Dir is the folder the files went into.
	Dir string
	// Downloaded, Skipped and Failed count tracks. Skipped ones were already on disk or in
	// the history.
	Downloaded, Skipped, Failed int
}

func (r *Result) add(o Result) {
	r.Downloaded += o.Downloaded
	r.Skipped += o.Skipped
	r.Failed += o.Failed
}

// AlbumByID fetches an album's listing and downloads it.
func (d *Downloader) AlbumByID(ctx context.Context, id string) (Result, error) {
	album, err := d.opts.Client.Album(ctx, id)
	if err != nil {
		return Result{}, err
	}
	return d.Album(ctx, album)
}

// Album downloads a whole album into a folder named by the folder template.
//
// One track that cannot be fetched does not abandon the rest: an album missing a track is
// still worth having, and the failures are returned together so the reason each file is
// absent survives.
func (d *Downloader) Album(ctx context.Context, album *qobuz.Album) (Result, error) {
	if d.opts.History.Has("album", album.ID) {
		d.printf("skipping %s - %s: already downloaded (see history)\n", album.Artist, album.FullTitle())
		return Result{Skipped: len(album.Tracks)}, nil
	}
	format := qobuz.Clamp(d.opts.Quality, album)
	dir := filepath.Join(d.opts.Directory, d.folderName(album, format))
	d.printf("%s - %s (%s) → %s\n", album.Artist, album.FullTitle(), format, dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("download: %w", err)
	}
	cover := d.cover(ctx, album, dir)

	result := Result{Dir: dir}
	var failures []error
	var names []string
	perMedium := trackTotals(album)
	for i := range album.Tracks {
		track := &album.Tracks[i]
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if !track.Streamable {
			d.printf("  [%d/%d] %s: not streamable on this account\n", i+1, len(album.Tracks), track.FullTitle())
			result.Failed++
			failures = append(failures, fmt.Errorf("%s: %w", track.FullTitle(), qobuz.ErrUnavailable))
			continue
		}
		rel := d.trackPath(album, track, format, perMedium[track.Medium], naming.Variables{})
		names = append(names, rel)
		outcome, err := d.fetchTrack(ctx, track, album, format, filepath.Join(dir, rel), cover,
			fmt.Sprintf("[%d/%d]", i+1, len(album.Tracks)), tagsFor(album, track, perMedium[track.Medium]))
		result.add(outcome)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", rel, err))
		}
	}

	if d.opts.NoCover {
		_ = os.Remove(filepath.Join(dir, CoverName))
	}
	if d.opts.AlbumM3U && len(names) > 0 {
		if err := writeM3U(filepath.Join(dir, filepath.Base(dir)+".m3u"), names); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) == 0 {
		if err := d.opts.History.Add("album", album.ID); err != nil {
			return result, err
		}
	}
	return result, errors.Join(failures...)
}

// TrackByID fetches one track and downloads it into its album's folder.
func (d *Downloader) TrackByID(ctx context.Context, id string) (Result, error) {
	track, err := d.opts.Client.Track(ctx, id)
	if err != nil {
		return Result{}, err
	}
	return d.Track(ctx, track)
}

// Track downloads one track into the folder its album would have, with the album's cover.
func (d *Downloader) Track(ctx context.Context, track *qobuz.Track) (Result, error) {
	if track.Album == nil {
		return Result{}, fmt.Errorf("download: track %s carries no album", track.ID)
	}
	if d.opts.History.Has("track", track.ID) {
		d.printf("skipping %s: already downloaded (see history)\n", track.FullTitle())
		return Result{Skipped: 1}, nil
	}
	album := track.Album
	format := qobuz.Clamp(d.opts.Quality, album)
	dir := filepath.Join(d.opts.Directory, d.folderName(album, format))
	d.printf("%s - %s (%s) → %s\n", album.Artist, track.FullTitle(), format, dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("download: %w", err)
	}
	cover := d.cover(ctx, album, dir)

	rel := d.trackPath(album, track, format, album.TrackCount, naming.Variables{})
	result, err := d.fetchTrack(ctx, track, album, format, filepath.Join(dir, rel), cover, "", tagsFor(album, track, album.TrackCount))
	result.Dir = dir
	if d.opts.NoCover {
		_ = os.Remove(filepath.Join(dir, CoverName))
	}
	if err == nil && result.Failed == 0 {
		if err := d.opts.History.Add("track", track.ID); err != nil {
			return result, err
		}
	}
	return result, err
}

// PlaylistByID fetches a playlist and downloads it.
func (d *Downloader) PlaylistByID(ctx context.Context, id string) (Result, error) {
	playlist, err := d.opts.Client.Playlist(ctx, id)
	if err != nil {
		return Result{}, err
	}
	return d.Playlist(ctx, playlist)
}

// Playlist downloads every track into one folder named after the playlist, numbered in
// playlist order, and writes an .m3u beside them unless told not to. The tracks are tagged
// with their own albums, so the folder plays as a playlist and each file still says where
// it came from.
func (d *Downloader) Playlist(ctx context.Context, playlist *qobuz.Playlist) (Result, error) {
	dir := filepath.Join(d.opts.Directory, naming.Sanitize(playlist.Name))
	d.printf("playlist %s (%d tracks) → %s\n", playlist.Name, len(playlist.Tracks), dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("download: %w", err)
	}

	result := Result{Dir: dir}
	var failures []error
	var names []string
	for i := range playlist.Tracks {
		track := &playlist.Tracks[i]
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if track.Album == nil {
			failures = append(failures, fmt.Errorf("%s: carries no album", track.FullTitle()))
			result.Failed++
			continue
		}
		if !track.Streamable {
			d.printf("  [%d/%d] %s: not streamable on this account\n", i+1, len(playlist.Tracks), track.FullTitle())
			result.Failed++
			failures = append(failures, fmt.Errorf("%s: %w", track.FullTitle(), qobuz.ErrUnavailable))
			continue
		}
		album := track.Album
		format := qobuz.Clamp(d.opts.Quality, album)
		extra := naming.Variables{
			"playlist":      playlist.Name,
			"playlistindex": naming.Number(i+1, len(playlist.Tracks)),
			"tracknumber":   naming.Number(i+1, len(playlist.Tracks)),
		}
		rel := d.trackPath(album, track, format, album.TrackCount, extra)
		// Flat: a playlist is one folder, whatever discs its tracks came from.
		rel = filepath.Base(rel)
		names = append(names, rel)

		var cover []byte
		if d.opts.EmbedArt && album.Image != "" {
			cover = d.coverBytes(ctx, album, dir)
		}
		outcome, err := d.fetchTrack(ctx, track, album, format, filepath.Join(dir, rel), cover,
			fmt.Sprintf("[%d/%d]", i+1, len(playlist.Tracks)), tagsFor(album, track, album.TrackCount))
		result.add(outcome)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", rel, err))
		}
	}
	if !d.opts.NoPlaylistM3U && len(names) > 0 {
		if err := writeM3U(filepath.Join(dir, naming.Sanitize(playlist.Name)+".m3u"), names); err != nil {
			failures = append(failures, err)
		}
	}
	return result, errors.Join(failures...)
}

// ArtistByID downloads an artist's discography, one folder per album.
func (d *Downloader) ArtistByID(ctx context.Context, id string) (Result, error) {
	artist, err := d.opts.Client.Artist(ctx, id)
	if err != nil {
		return Result{}, err
	}
	d.printf("artist %s: %d albums\n", artist.Name, len(artist.Albums))
	return d.albums(ctx, artist.Albums, artist.Name)
}

// LabelByID downloads a label's whole catalogue, one folder per album.
func (d *Downloader) LabelByID(ctx context.Context, id string) (Result, error) {
	label, err := d.opts.Client.Label(ctx, id)
	if err != nil {
		return Result{}, err
	}
	d.printf("label %s: %d albums\n", label.Name, len(label.Albums))
	return d.albums(ctx, label.Albums, "")
}

// albums downloads a discography. Each album's listing is read first, since what the
// artist or label page carries has no tracks and, on the live API, no release type either.
func (d *Downloader) albums(ctx context.Context, albums []qobuz.Album, artist string) (Result, error) {
	var result Result
	var failures []error
	for i := range albums {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		summary := &albums[i]
		if !summary.Streamable {
			d.printf("skipping %s: not streamable\n", summary.FullTitle())
			continue
		}
		if d.opts.History.Has("album", summary.ID) {
			d.printf("skipping %s: already downloaded (see history)\n", summary.FullTitle())
			result.Skipped++
			continue
		}
		full, err := d.opts.Client.Album(ctx, summary.ID)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", summary.FullTitle(), err))
			continue
		}
		if d.opts.AlbumsOnly && !IsAlbum(full, artist) {
			d.printf("skipping %s: not a full album by %s\n", full.FullTitle(), orText(artist, "one artist"))
			continue
		}
		outcome, err := d.Album(ctx, full)
		result.add(outcome)
		if err != nil {
			failures = append(failures, err)
		}
	}
	return result, errors.Join(failures...)
}

var epOrSingle = regexp.MustCompile(`(?i)(\bEP\b|\(single\)|- single\b|\bsingles?\b)`)

// IsAlbum reports whether a release counts as a proper album by the named artist: not a
// single or an EP, and not a compilation credited to various artists. Qobuz's own release
// type is used where it says one; the title is read where it does not. artist may be empty,
// in which case only the various-artists check is skipped.
func IsAlbum(album *qobuz.Album, artist string) bool {
	switch strings.ToLower(album.ReleaseType) {
	case "", "album":
	default:
		return false
	}
	if album.ReleaseType == "" && epOrSingle.MatchString(album.FullTitle()) {
		return false
	}
	if strings.EqualFold(album.Artist, "Various Artists") {
		return false
	}
	if artist != "" && !strings.EqualFold(album.Artist, artist) {
		return false
	}
	return true
}

// fetchTrack writes one track, tags it, and reports what happened on Out. A file already
// there is left alone: it was either downloaded before or put there on purpose.
func (d *Downloader) fetchTrack(ctx context.Context, track *qobuz.Track, album *qobuz.Album, format qobuz.Format,
	path string, cover []byte, label string, tags *tag.Tags) (Result, error) {
	name := filepath.Base(path)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		d.printf("  %s %s: already on disk\n", label, name)
		return Result{Skipped: 1}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Result{Failed: 1}, err
	}

	progress := d.progress(label, name)
	file, err := d.opts.Client.Fetch(ctx, track.ID, format, path, progress.report)
	if err != nil {
		progress.done("failed: " + err.Error())
		return Result{Failed: 1}, err
	}

	if d.opts.EmbedArt {
		tags.Cover = cover
	}
	if err := tag.Write(path, tags); err != nil {
		progress.done("downloaded, but not tagged: " + err.Error())
		return Result{Downloaded: 1}, err
	}
	progress.done(describe(file))
	return Result{Downloaded: 1}, nil
}

func describe(file *qobuz.File) string {
	if file.BitDepth > 0 && file.SampleRate > 0 {
		return fmt.Sprintf("%s %d-bit/%skHz", file.Format, file.BitDepth, rate(file.SampleRate))
	}
	return file.Format.String()
}

// cover fetches the album's artwork into dir and returns its bytes when they are wanted for
// embedding. Artwork is not worth failing a download for.
func (d *Downloader) cover(ctx context.Context, album *qobuz.Album, dir string) []byte {
	if album.Image == "" {
		return nil
	}
	path := filepath.Join(dir, CoverName)
	if _, err := os.Stat(path); err != nil {
		if err := d.opts.Client.SaveImage(ctx, album.Image, d.opts.CoverSize, path); err != nil {
			d.printf("  cover: %v\n", err)
			return nil
		}
	}
	if !d.opts.EmbedArt {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// coverBytes fetches an album's artwork for embedding only, through a temporary file that
// does not stay: a playlist folder holds tracks from many albums and no cover of its own.
func (d *Downloader) coverBytes(ctx context.Context, album *qobuz.Album, dir string) []byte {
	path := filepath.Join(dir, ".cover-"+naming.Sanitize(album.ID)+".jpg")
	defer func() { _ = os.Remove(path) }()
	if err := d.opts.Client.SaveImage(ctx, album.Image, d.opts.CoverSize, path); err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// folderName expands the folder template for an album at the quality it will be fetched at.
func (d *Downloader) folderName(album *qobuz.Album, format qobuz.Format) string {
	template := d.opts.FolderFormat
	if format == qobuz.FormatMP3 && template == naming.DefaultFolder {
		template = DefaultFolderMP3
	}
	return naming.Expand(template, d.variables(album, nil, format, 0))
}

// trackPath is the file's path inside the album folder: a "Disc N" folder on a multi-disc
// album, then the track template and the container's extension.
func (d *Downloader) trackPath(album *qobuz.Album, track *qobuz.Track, format qobuz.Format, total int, extra naming.Variables) string {
	vars := d.variables(album, track, format, total)
	for k, v := range extra {
		vars[k] = v
	}
	name := naming.Expand(d.opts.TrackFormat, vars) + "." + format.Extension()
	if album.MediaCount > 1 && len(extra) == 0 {
		return filepath.Join("Disc "+strconv.Itoa(track.Medium), name)
	}
	return name
}

// variables is everything a template may name, for an album and optionally one track of it.
func (d *Downloader) variables(album *qobuz.Album, track *qobuz.Track, format qobuz.Format, total int) naming.Variables {
	depth, sampleRate := delivered(album, format)
	vars := naming.Variables{
		"artist":        album.Artist,
		"albumartist":   album.Artist,
		"album":         album.FullTitle(),
		"version":       album.Version,
		"year":          album.Year(),
		"date":          album.Date,
		"label":         album.Label,
		"genre":         album.Genre,
		"bit_depth":     strconv.Itoa(depth),
		"sampling_rate": rate(sampleRate),
		"quality":       format.String(),
		"id":            album.ID,
		"upc":           album.UPC,
		"disctotal":     strconv.Itoa(max(album.MediaCount, 1)),
		"tracktotal":    strconv.Itoa(total),
	}
	if track != nil {
		vars["tracknumber"] = naming.Number(track.Position, total)
		vars["tracktitle"] = track.FullTitle()
		vars["trackartist"] = orText(track.Performer, album.Artist)
		vars["composer"] = track.Composer
		vars["discnumber"] = strconv.Itoa(max(track.Medium, 1))
	}
	return vars
}

// delivered is the bit depth and sampling rate the files will carry: the master's, lowered
// to what the format allows.
func delivered(album *qobuz.Album, format qobuz.Format) (int, float64) {
	depth, sampleRate := album.MaxBitDepth, album.MaxSampleRate
	if depth == 0 {
		depth = 16
	}
	if sampleRate == 0 {
		sampleRate = 44.1
	}
	switch format {
	case qobuz.FormatMP3, qobuz.FormatFLAC:
		return 16, 44.1
	case qobuz.FormatFLAC96:
		return min(depth, 24), min(sampleRate, 96)
	}
	return depth, sampleRate
}

// rate renders a sampling rate the short way: 44.1, 48, 96, 192.
func rate(kHz float64) string {
	return strconv.FormatFloat(kHz, 'f', -1, 64)
}

// tagsFor is what a track is tagged with, from what Qobuz said about it and its album.
func tagsFor(album *qobuz.Album, track *qobuz.Track, total int) *tag.Tags {
	return &tag.Tags{
		Title:       track.FullTitle(),
		Artist:      orText(track.Performer, album.Artist),
		Album:       album.FullTitle(),
		AlbumArtist: album.Artist,
		Composer:    track.Composer,
		Genre:       album.Genre,
		Date:        album.Date,
		Label:       album.Label,
		Copyright:   track.Copyright,
		ISRC:        track.ISRC,
		UPC:         album.UPC,
		TrackNumber: track.Position,
		TrackTotal:  total,
		DiscNumber:  max(track.Medium, 1),
		DiscTotal:   max(album.MediaCount, 1),
	}
}

// trackTotals counts the tracks on each medium, which is what TRACKTOTAL means on a
// multi-disc album.
func trackTotals(album *qobuz.Album) map[int]int {
	totals := map[int]int{}
	for _, track := range album.Tracks {
		totals[track.Medium]++
	}
	return totals
}

// writeM3U lists the given files, relative to the playlist's own folder.
func writeM3U(path string, names []string) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for _, name := range names {
		b.WriteString(filepath.ToSlash(name))
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("download: write %s: %w", path, err)
	}
	return nil
}

func orText(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (d *Downloader) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(d.opts.Out, format, args...)
}

// progressLine is one track's line of output: redrawn in place on a terminal, printed once
// when it starts and once when it ends otherwise.
type progressLine struct {
	d     *Downloader
	label string
	name  string
	last  time.Time
}

func (d *Downloader) progress(label, name string) *progressLine {
	p := &progressLine{d: d, label: label, name: name}
	if !d.opts.Interactive {
		d.printf("  %s %s ...\n", label, name)
	}
	return p
}

func (p *progressLine) report(written, total int64) {
	if !p.d.opts.Interactive || time.Since(p.last) < 100*time.Millisecond {
		return
	}
	p.last = time.Now()
	if total > 0 {
		p.d.printf("\r  %s %s  %3d%%  %s", p.label, p.name, written*100/total, size(written))
	} else {
		p.d.printf("\r  %s %s  %s", p.label, p.name, size(written))
	}
}

func (p *progressLine) done(outcome string) {
	if p.d.opts.Interactive {
		p.d.printf("\r\033[K  %s %s  %s\n", p.label, p.name, outcome)
		return
	}
	p.d.printf("  %s %s  %s\n", p.label, p.name, outcome)
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
