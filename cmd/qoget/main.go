// Command qoget downloads music from Qobuz: albums, tracks, playlists, whole artist or
// label discographies, from a URL or a search.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/davidetoniatti/qoget/internal/config"
	"github.com/davidetoniatti/qoget/internal/download"
	"github.com/davidetoniatti/qoget/internal/history"
	"github.com/davidetoniatti/qoget/internal/qobuz"
)

const usage = `qoget downloads music from Qobuz.

Usage:
  qoget <command> [flags] [arguments]

Commands:
  dl       download albums, tracks, playlists, artists or labels from Qobuz URLs
  search   search Qobuz and pick what to download (alias: fun)
  lucky    download the first results of a search without asking
  info     show what a URL points at, without downloading
  login    store the account token and check it
  config   show or change the settings
  history  show or purge the list of what has been downloaded
  version  print the version

Run "qoget <command> -h" for the flags of one command.

To login, see "qoget login -h".
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "qoget:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, usage)
		return nil
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	env := &environment{stdin: stdin, stdout: stdout, stderr: stderr, configPath: config.Path()}
	switch command, rest := args[0], args[1:]; command {
	case "dl", "download":
		return env.download(ctx, rest)
	case "search", "fun":
		return env.search(ctx, rest)
	case "lucky":
		return env.lucky(ctx, rest)
	case "info":
		return env.info(ctx, rest)
	case "login":
		return env.login(ctx, rest)
	case "config":
		return env.configure(rest)
	case "history":
		return env.history(rest)
	case "version":
		fmt.Fprintln(stdout, versionString())
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", command)
	}
}

// environment is what every command runs against.
type environment struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	configPath     string
}

// downloadFlags are the flags the downloading commands share. Each starts from the
// configuration, so a flag typed is a one-off and one saved with "config set" is the norm.
type downloadFlags struct {
	cfg          *config.Config
	quality      string
	directory    string
	embedArt     bool
	noCover      bool
	ogCover      bool
	coverSize    string
	albumsOnly   bool
	noHistory    bool
	albumM3U     bool
	noM3U        bool
	noFallback   bool
	folderFormat string
	trackFormat  string
}

func (env *environment) newFlagSet(name, synopsis string) (*flag.FlagSet, *downloadFlags, error) {
	cfg, err := config.Load(env.configPath)
	if err != nil {
		return nil, nil, err
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() {
		fmt.Fprintf(env.stderr, "Usage: qoget %s\n\nFlags:\n", synopsis)
		fs.PrintDefaults()
	}

	f := &downloadFlags{cfg: cfg}
	fs.StringVar(&f.quality, "q", cfg.Quality, "quality to ask for: flac-24-192, flac-24-96, flac-16, mp3-320 (or 27, 7, 6, 5)")
	fs.StringVar(&f.directory, "d", cfg.Directory, "directory downloads go into")
	fs.BoolVar(&f.embedArt, "embed-art", cfg.EmbedArt, "embed the cover in every track")
	fs.BoolVar(&f.noCover, "no-cover", cfg.NoCover, "leave no cover.jpg beside the tracks")
	fs.BoolVar(&f.ogCover, "og-cover", false, "fetch the cover as the label supplied it (same as -cover-size org)")
	fs.StringVar(&f.coverSize, "cover-size", cfg.CoverSize, "cover rendition: max (1400px), org (original), large (600px)")
	fs.BoolVar(&f.albumsOnly, "albums-only", cfg.AlbumsOnly, "for artists and labels: skip singles, EPs and various-artists compilations")
	fs.BoolVar(&f.noHistory, "no-db", cfg.NoHistory, "ignore the history of what was downloaded (and do not add to it)")
	fs.BoolVar(&f.albumM3U, "m3u", cfg.AlbumM3U, "write an .m3u beside every album")
	fs.BoolVar(&f.noM3U, "no-m3u", cfg.NoM3U, "write no .m3u for playlists")
	fs.BoolVar(&f.noFallback, "no-fallback", cfg.NoFallback, "refuse a lower quality than asked for instead of taking the best available")
	fs.StringVar(&f.folderFormat, "folder-format", cfg.FolderFormat, "template for the album folder")
	fs.StringVar(&f.trackFormat, "track-format", cfg.TrackFormat, "template for the track file")
	return fs, f, nil
}

// session is a configured client and downloader.
type session struct {
	client     *qobuz.Client
	downloader *download.Downloader
	history    *history.Store
}

func (env *environment) session(f *downloadFlags) (*session, error) {
	if f.cfg.AuthToken == "" {
		return nil, errors.New("no account token: run \"qoget login\" first (or set QOGET_AUTH_TOKEN)")
	}
	format, err := qobuz.ParseFormat(f.quality)
	if err != nil {
		return nil, err
	}
	if f.ogCover {
		f.coverSize = string(qobuz.CoverOriginal)
	}
	coverSize, err := qobuz.ParseCoverSize(f.coverSize)
	if err != nil {
		return nil, err
	}

	var store *history.Store
	if !f.noHistory {
		if store, err = history.Open(config.HistoryPath()); err != nil {
			return nil, err
		}
	}

	client := qobuz.New(qobuz.Options{
		AuthToken:  f.cfg.AuthToken,
		NoFallback: f.noFallback,
		AppID:      f.cfg.AppID,
		Secrets:    f.cfg.Secrets,
	})
	downloader, err := download.New(download.Options{
		Client:        client,
		Directory:     f.directory,
		Quality:       format,
		FolderFormat:  f.folderFormat,
		TrackFormat:   f.trackFormat,
		EmbedArt:      f.embedArt,
		NoCover:       f.noCover,
		CoverSize:     coverSize,
		AlbumM3U:      f.albumM3U,
		NoPlaylistM3U: f.noM3U,
		AlbumsOnly:    f.albumsOnly,
		History:       store,
		Out:           env.stdout,
		Interactive:   isTerminal(env.stdout),
	})
	if err != nil {
		return nil, err
	}
	return &session{client: client, downloader: downloader, history: store}, nil
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// report prints what a run did and turns its failures into the exit status.
func report(out io.Writer, result download.Result, err error) error {
	fmt.Fprintf(out, "\n%d downloaded, %d skipped, %d failed\n", result.Downloaded, result.Skipped, result.Failed)
	if err != nil {
		fmt.Fprintln(out)
		for _, line := range strings.Split(err.Error(), "\n") {
			fmt.Fprintln(out, "  "+line)
		}
		return errors.New("some downloads failed")
	}
	return nil
}

// version is set by the linker on a release build (-X main.version=...); a "go install"
// from the module proxy carries its version in the build info instead.
var version = "dev"

func versionString() string {
	v := version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	return "qoget " + v + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
