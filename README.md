# qoget

[![CI](https://github.com/davidetoniatti/qoget/actions/workflows/ci.yml/badge.svg)](https://github.com/davidetoniatti/qoget/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/davidetoniatti/qoget.svg)](https://pkg.go.dev/github.com/davidetoniatti/qoget)
[![Go Report Card](https://goreportcard.com/badge/github.com/davidetoniatti/qoget)](https://goreportcard.com/report/github.com/davidetoniatti/qoget)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Command-line downloader for Qobuz, written in Go. Downloads albums, tracks, playlists and whole
artist or label discographies as tagged FLAC or MP3 files, from a URL or a search.

## Features

- Download albums, tracks, playlists, artists and labels from any Qobuz URL (store, web player, share links)
- Hi-Res FLAC up to 24-bit/192 kHz, FLAC 16-bit, MP3 320 kbps
- Interactive search (`search`) and first-result download (`lucky`) for albums, tracks, artists and playlists
- Tags from Qobuz metadata: Vorbis comments on FLAC, ID3v2.4 on MP3, optional embedded cover
- Cover art in three sizes (1400 px, original, 600 px)
- Configurable folder and file name templates, `Disc N` folders for multi-disc albums, `.m3u` playlists
- Download history: nothing is fetched twice unless asked
- Retries on network failures; half-written files never appear under their final name
- Single static binary, no runtime dependencies

## Installation

### Go

```sh
go install github.com/davidetoniatti/qoget/cmd/qoget@latest
```

Requires Go 1.25 or newer. The binary is installed in `$(go env GOPATH)/bin`.

### Binaries

Pre-built binaries for Linux, macOS and Windows (amd64, arm64) are attached to each
[release](https://github.com/davidetoniatti/qoget/releases).

### From source

```sh
git clone https://github.com/davidetoniatti/qoget.git
cd qoget
make build      # ./qoget
make install    # $(go env GOPATH)/bin/qoget
```

## Getting started

Qobuz no longer accepts email/password login from third-party clients (since April 2026 the
web player authenticates through OAuth with a captcha). qoget authenticates with the
`user_auth_token` of a browser session:

1. Log in at <https://play.qobuz.com>.
2. Open the browser developer tools, **Network** tab, filter on `user/login`.
3. Reload <https://play.qobuz.com/login> and select the `login` request.
4. In its **Response**, copy the value of `user_auth_token`.
5. Run `qoget login` and paste the token.

The token is validated against the account and stored in the configuration file. It is a JWT
and expires: when downloads fail with `401`, repeat the steps above.

```sh
qoget login                 # prompts for the token
qoget login -token TOKEN    # non-interactive
```

## Usage

```
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
```

### Download from URLs

```sh
qoget dl https://www.qobuz.com/it-it/album/some-album-some-artist/abcdefghijklm
qoget dl https://open.qobuz.com/track/200000001 -q mp3-320
qoget dl https://www.qobuz.com/it-it/interpreter/some-artist/123456 -albums-only
qoget dl https://open.qobuz.com/playlist/3000001 -embed-art
qoget dl -i urls.txt                # one URL per line, # comments allowed
qoget dl album:abcdefghijklm        # kind:id shorthand
```

Accepted URL forms:

| Kind | URL |
| --- | --- |
| album | `www.qobuz.com/<locale>/album/<slug>/<id>`, `open.qobuz.com/album/<id>`, `play.qobuz.com/album/<id>` |
| track | `www.qobuz.com/<locale>/track/<id>`, `open.qobuz.com/track/<id>` |
| artist | `www.qobuz.com/<locale>/interpreter/<slug>/<id>`, `open.qobuz.com/artist/<id>` |
| label | `www.qobuz.com/<locale>/label/<slug>/<id>` |
| playlist | `www.qobuz.com/<locale>/playlist/<slug>/<id>`, `open.qobuz.com/playlist/<id>` |

### Search

```sh
qoget search artist name album title      # albums; pick with "1,3-5", "all" or "q"
qoget search -t track -l 30 song title
qoget search -t artist artist name
qoget lucky -n 2 artist name              # download the first two album results
qoget lucky -t track -q flac-16 song title artist name
```

### Inspect

```sh
qoget info https://www.qobuz.com/it-it/album/another-album-another-artist/0000000000003
```

### Flags

Available on `dl`, `search`, `lucky` and `info`. Each defaults to the configuration value in
the second column.

| Flag | Config key | Description |
| --- | --- | --- |
| `-q` | `quality` | `flac-24-192` (default), `flac-24-96`, `flac-16`, `mp3-320`; Qobuz ids `27`, `7`, `6`, `5` also accepted |
| `-d` | `directory` | Output directory (default `~/Music/Qobuz Downloads`) |
| `-embed-art` | `embed_art` | Embed the cover in every track |
| `-no-cover` | `no_cover` | Do not save `cover.jpg` |
| `-cover-size` | `cover_size` | `max` (1400 px, default), `org` (original), `large` (600 px) |
| `-og-cover` | | Shorthand for `-cover-size org` |
| `-albums-only` | `albums_only` | Artists/labels: skip singles, EPs and various-artists compilations |
| `-no-db` | `no_history` | Ignore and do not update the download history |
| `-m3u` | `album_m3u` | Write an `.m3u` for every album |
| `-no-m3u` | `no_playlist_m3u` | Do not write an `.m3u` for playlists |
| `-no-fallback` | `no_fallback` | Fail instead of accepting a lower quality than requested |
| `-folder-format` | `folder_format` | Album folder template |
| `-track-format` | `track_format` | Track file name template |
| `-i` | | (`dl` only) File of URLs, one per line |

The requested quality is a ceiling: Qobuz serves what the master has, and the folder is named
for what was delivered. FLAC never falls back to MP3.

## Configuration

File: `~/.config/qoget/config.json` (`$XDG_CONFIG_HOME/qoget/config.json`, or the path in
`$QOGET_CONFIG`). Created by `qoget login` with mode `0600`.

```sh
qoget config show                       # token and secrets redacted
qoget config set quality flac-24-96
qoget config set directory ~/Music/Qobuz
qoget config set embed_art true
qoget config reset                      # defaults, token kept
```

Keys: `auth_token`, `directory`, `quality`, `limit`, `folder_format`, `track_format`,
`embed_art`, `no_cover`, `cover_size`, `album_m3u`, `no_playlist_m3u`, `no_fallback`,
`albums_only`, `no_history`.

Environment overrides: `QOGET_AUTH_TOKEN`, `QOGET_QUALITY`, `QOGET_DIRECTORY`, `QOGET_CONFIG`.

### Naming templates

Defaults:

```
folder_format  {artist} - {album} ({year}) [{bit_depth}B-{sampling_rate}kHz]
track_format   {tracknumber}. {tracktitle}
```

For MP3 the default folder template is `{artist} - {album} ({year}) [MP3-320]`.

Variables: `artist`, `albumartist`, `album`, `version`, `year`, `date`, `label`, `genre`,
`bit_depth`, `sampling_rate`, `quality`, `id`, `upc`, `tracknumber`, `tracktitle`,
`trackartist`, `composer`, `discnumber`, `disctotal`, `tracktotal`, `playlist`, `playlistindex`.

Names are sanitised for every filesystem. Multi-disc albums get `Disc N` subfolders; playlists
are one flat folder with tracks numbered in playlist order.

### Tags

| Field | FLAC | MP3 |
| --- | --- | --- |
| title, artist, album, album artist | `TITLE`, `ARTIST`, `ALBUM`, `ALBUMARTIST` | `TIT2`, `TPE1`, `TALB`, `TPE2` |
| composer, genre, date | `COMPOSER`, `GENRE`, `DATE`/`YEAR` | `TCOM`, `TCON`, `TDRC` |
| label, copyright | `LABEL`/`ORGANIZATION`, `COPYRIGHT` | `TPUB`, `TCOP` |
| ISRC, UPC | `ISRC`, `BARCODE` | `TSRC`, `TXXX:BARCODE` |
| track, disc | `TRACKNUMBER`/`TRACKTOTAL`, `DISCNUMBER`/`DISCTOTAL` | `TRCK`, `TPOS` |
| cover (`-embed-art`) | `PICTURE` block | `APIC` |

### History

`~/.config/qoget/downloaded.txt`, one `kind:id` per line. Completed albums and tracks are
recorded and skipped on later runs; an album with a failed track is not recorded, so a rerun
completes it. Files already present on disk are never re-downloaded.

```sh
qoget history           # list
qoget history -purge    # forget everything
```

## Development

```sh
make check    # gofmt, go vet, go test -race
make build
```

Tests run against an in-process stub of the Qobuz API (`internal/qobuz/qobuztest`); nothing in
the test suite touches the network. CI runs on Linux, macOS and Windows. Tagging `vX.Y.Z`
builds and publishes release binaries with GoReleaser.

## Disclaimer

Use qoget only with an account you own and content you are entitled to download under
Qobuz's terms of service. This project is not affiliated with Qobuz.

## License

[MIT](LICENSE)
