package tag

import (
	"github.com/bogem/id3v2/v2"
)

// writeMP3 writes an ID3v2.4 tag, replacing whatever the file carried: Qobuz's MP3s arrive
// with a tag of their own, and two tags with different answers is worse than one.
func writeMP3(path string, t *Tags) error {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: false})
	if err != nil {
		return err
	}
	defer func() { _ = tag.Close() }()

	tag.SetVersion(4)
	encoding := id3v2.EncodingUTF8
	text := func(id, value string) {
		if value != "" {
			tag.AddFrame(id, id3v2.TextFrame{Encoding: encoding, Text: value})
		}
	}
	txxx := func(description, value string) {
		if value != "" {
			tag.AddFrame("TXXX", id3v2.UserDefinedTextFrame{
				Encoding: encoding, Description: description, Value: value,
			})
		}
	}

	text("TIT2", t.Title)
	text("TPE1", t.Artist)
	text("TALB", t.Album)
	text("TPE2", t.AlbumArtist)
	text("TCOM", t.Composer)
	text("TCON", t.Genre)
	text("TDRC", t.Date)
	text("TPUB", t.Label)
	text("TCOP", t.Copyright)
	text("TSRC", t.ISRC)
	text("TRCK", position(t.TrackNumber, t.TrackTotal))
	text("TPOS", position(t.DiscNumber, t.DiscTotal))
	txxx("BARCODE", t.UPC)

	if len(t.Cover) > 0 {
		tag.AddAttachedPicture(id3v2.PictureFrame{
			Encoding:    encoding,
			MimeType:    t.coverMIME(),
			PictureType: id3v2.PTFrontCover,
			Picture:     t.Cover,
		})
	}
	return tag.Save()
}
