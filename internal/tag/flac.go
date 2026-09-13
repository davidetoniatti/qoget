package tag

import (
	"fmt"

	"github.com/go-flac/flacpicture"
	"github.com/go-flac/flacvorbis"
	"github.com/go-flac/go-flac"
)

// writeFLAC replaces the Vorbis comment and picture blocks and saves the file. go-flac's Save
// rewrites the whole file, which is fine here: the file is our own download, not somebody's
// library, and an interrupted write is a download to redo.
func writeFLAC(path string, t *Tags) error {
	f, err := flac.ParseFile(path)
	if err != nil {
		return err
	}

	comment := flacvorbis.New()
	set := func(key, value string) {
		if value != "" {
			_ = comment.Add(key, value)
		}
	}
	set("TITLE", t.Title)
	set("ARTIST", t.Artist)
	set("ALBUM", t.Album)
	set("ALBUMARTIST", t.AlbumArtist)
	set("COMPOSER", t.Composer)
	set("GENRE", t.Genre)
	set("DATE", t.Date)
	set("YEAR", t.Year())
	set("LABEL", t.Label)
	set("ORGANIZATION", t.Label)
	set("COPYRIGHT", t.Copyright)
	set("ISRC", t.ISRC)
	set("BARCODE", t.UPC)
	set("TRACKNUMBER", itoa(t.TrackNumber))
	// Both spellings of the totals, since readers in the wild expect either.
	set("TRACKTOTAL", itoa(t.TrackTotal))
	set("TOTALTRACKS", itoa(t.TrackTotal))
	set("DISCNUMBER", itoa(t.DiscNumber))
	set("DISCTOTAL", itoa(t.DiscTotal))
	set("TOTALDISCS", itoa(t.DiscTotal))

	f.Meta = withoutBlocks(f.Meta, flac.VorbisComment, flac.Picture)
	commentBlock := comment.Marshal()
	f.Meta = append(f.Meta, &commentBlock)

	if len(t.Cover) > 0 {
		picture, err := flacpicture.NewFromImageData(flacpicture.PictureTypeFrontCover, "", t.Cover, t.coverMIME())
		if err != nil {
			return fmt.Errorf("build picture block: %w", err)
		}
		pictureBlock := picture.Marshal()
		f.Meta = append(f.Meta, &pictureBlock)
	}
	return f.Save(path)
}

// withoutBlocks drops every block of the given types, so a rewrite replaces them instead of
// accumulating duplicates.
func withoutBlocks(meta []*flac.MetaDataBlock, types ...flac.BlockType) []*flac.MetaDataBlock {
	drop := make(map[flac.BlockType]struct{}, len(types))
	for _, t := range types {
		drop[t] = struct{}{}
	}
	kept := meta[:0]
	for _, block := range meta {
		if _, skip := drop[block.Type]; !skip {
			kept = append(kept, block)
		}
	}
	return kept
}
