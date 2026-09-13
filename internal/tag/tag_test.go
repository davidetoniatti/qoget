package tag_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bogem/id3v2/v2"
	"github.com/go-flac/flacpicture"
	"github.com/go-flac/flacvorbis"
	"github.com/go-flac/go-flac"

	"github.com/davidetoniatti/qoget/internal/qobuz/qobuztest"
	"github.com/davidetoniatti/qoget/internal/tag"
)

func sample() *tag.Tags {
	return &tag.Tags{
		Title: "Opening Track", Artist: "Some Band", Album: "First Album", AlbumArtist: "Some Band",
		Composer: "A Composer", Genre: "Metal", Date: "2023-03-03", Label: "Some Label",
		Copyright: "(P) 2023 Some Label", ISRC: "XX0000000001", UPC: "0000000000002",
		TrackNumber: 1, TrackTotal: 9, DiscNumber: 1, DiscTotal: 1,
		Cover: qobuztest.FixtureBytes("cover.jpg"),
	}
}

func TestWriteFLAC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "01. Opening Track.flac")
	if err := os.WriteFile(path, qobuztest.FixtureBytes("blank.flac"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Twice, so that a rewrite is seen to replace the blocks rather than pile them up.
	for range 2 {
		if err := tag.Write(path, sample()); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var comments, pictures int
	fields := map[string]string{}
	for _, block := range f.Meta {
		switch block.Type {
		case flac.VorbisComment:
			comments++
			c, err := flacvorbis.ParseFromMetaDataBlock(*block)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range c.Comments {
				k, v, _ := strings.Cut(line, "=")
				fields[k] = v
			}
		case flac.Picture:
			pictures++
			p, err := flacpicture.ParseFromMetaDataBlock(*block)
			if err != nil {
				t.Fatal(err)
			}
			if p.MIME != "image/jpeg" || len(p.ImageData) != len(qobuztest.FixtureBytes("cover.jpg")) {
				t.Errorf("picture = %s, %d bytes", p.MIME, len(p.ImageData))
			}
		}
	}
	if comments != 1 || pictures != 1 {
		t.Errorf("blocks: %d comment, %d picture; want one of each", comments, pictures)
	}
	for k, want := range map[string]string{
		"TITLE": "Opening Track", "ARTIST": "Some Band", "ALBUM": "First Album", "ALBUMARTIST": "Some Band",
		"COMPOSER": "A Composer", "GENRE": "Metal", "DATE": "2023-03-03", "YEAR": "2023",
		"LABEL": "Some Label", "ISRC": "XX0000000001", "BARCODE": "0000000000002",
		"TRACKNUMBER": "1", "TRACKTOTAL": "9", "TOTALTRACKS": "9", "DISCNUMBER": "1", "DISCTOTAL": "1",
	} {
		if fields[k] != want {
			t.Errorf("%s = %q, want %q", k, fields[k], want)
		}
	}
	if _, present := fields["TITLESORT"]; present {
		t.Error("an absent field must not be written")
	}
}

func TestWriteMP3(t *testing.T) {
	path := filepath.Join(t.TempDir(), "01. Opening Track.mp3")
	// An empty file is enough for id3v2 to put a tag in front of.
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tag.Write(path, sample()); err != nil {
		t.Fatalf("Write: %v", err)
	}

	read, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Close() }()

	if read.Title() != "Opening Track" || read.Artist() != "Some Band" || read.Album() != "First Album" || read.Genre() != "Metal" {
		t.Errorf("tag = %q / %q / %q / %q", read.Title(), read.Artist(), read.Album(), read.Genre())
	}
	if got := read.GetTextFrame("TRCK").Text; got != "1/9" {
		t.Errorf("TRCK = %q, want 1/9", got)
	}
	if got := read.GetTextFrame("TDRC").Text; got != "2023-03-03" {
		t.Errorf("TDRC = %q", got)
	}
	pictures := read.GetFrames(read.CommonID("Attached picture"))
	if len(pictures) != 1 {
		t.Fatalf("%d pictures, want 1", len(pictures))
	}
	if pf := pictures[0].(id3v2.PictureFrame); pf.MimeType != "image/jpeg" || len(pf.Picture) == 0 {
		t.Errorf("picture = %+v", pf.MimeType)
	}
}

func TestWriteRefusesOtherContainers(t *testing.T) {
	if err := tag.Write(filepath.Join(t.TempDir(), "x.ogg"), sample()); err == nil {
		t.Error("an unknown extension should be refused")
	}
}
