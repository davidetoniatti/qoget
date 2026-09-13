// Package tag writes the metadata Qobuz supplies into the files it delivers: Vorbis comments
// on FLAC, ID3v2.4 on MP3. Field names follow MusicBrainz Picard's conventions so that
// Picard, beets and the usual players read them.
package tag

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Tags is what one track is tagged with. Zero values are absent: an empty string or a zero
// number is never written.
type Tags struct {
	Title       string
	Artist      string // the track's own credit
	Album       string
	AlbumArtist string
	Composer    string
	Genre       string
	Date        string // ISO 8601, or a year alone
	Label       string
	Copyright   string
	ISRC        string
	UPC         string

	TrackNumber int
	TrackTotal  int
	DiscNumber  int
	DiscTotal   int

	// Cover is the front cover to embed, JPEG or PNG; nil embeds nothing.
	Cover []byte
}

// Write replaces the tags in path with t, leaving the audio untouched. The container is
// told by the extension.
func Write(path string, t *Tags) error {
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac":
		err = writeFLAC(path, t)
	case ".mp3":
		err = writeMP3(path, t)
	default:
		return fmt.Errorf("tag: %s: unsupported file type", path)
	}
	if err != nil {
		return fmt.Errorf("tag: write %s: %w", path, err)
	}
	return nil
}

// Year is the leading year of an ISO 8601 date, or the empty string.
func (t *Tags) Year() string {
	if len(t.Date) >= 4 {
		return t.Date[:4]
	}
	return ""
}

func (t *Tags) coverMIME() string {
	if strings.HasPrefix(string(t.Cover), "\x89PNG") {
		return "image/png"
	}
	return "image/jpeg"
}

// itoa renders a number for a tag value, "" for zero so absent fields are skipped.
func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// position renders a track or disc position for ID3, which combines number and total.
func position(number, total int) string {
	switch {
	case number == 0 && total == 0:
		return ""
	case total == 0:
		return strconv.Itoa(number)
	default:
		return fmt.Sprintf("%d/%d", number, total)
	}
}
