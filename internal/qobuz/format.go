package qobuz

import (
	"fmt"
	"strings"
)

// Format is a Qobuz format_id: the container and quality a stream is delivered in.
type Format int

const (
	FormatMP3    Format = 5  // MP3 320 kbps
	FormatFLAC   Format = 6  // FLAC 16-bit 44.1 kHz
	FormatFLAC96 Format = 7  // FLAC 24-bit up to 96 kHz
	FormatHiRes  Format = 27 // FLAC 24-bit up to 192 kHz
)

// DefaultFormat asks for the best Qobuz offers. What actually arrives is whatever the
// album has, since a request is clamped to the master it is made against.
const DefaultFormat = FormatHiRes

var formatNames = map[Format]string{
	FormatMP3:    "mp3-320",
	FormatFLAC:   "flac-16",
	FormatFLAC96: "flac-24-96",
	FormatHiRes:  "flac-24-192",
}

// ParseFormat maps a configured quality to a format id, accepting both the names used
// here and the bare numbers Qobuz itself uses. An empty string means the default.
//
// An unknown value is an error rather than the default: a typo in configuration should be
// reported, not silently answered with something else.
func ParseFormat(quality string) (Format, error) {
	quality = strings.ToLower(strings.TrimSpace(quality))
	if quality == "" {
		return DefaultFormat, nil
	}
	for format, name := range formatNames {
		if quality == name || quality == fmt.Sprint(int(format)) {
			return format, nil
		}
	}
	return 0, fmt.Errorf("qobuz: unknown quality %q", quality)
}

func (f Format) Valid() bool {
	_, known := formatNames[f]
	return known
}

func (f Format) String() string {
	if name, known := formatNames[f]; known {
		return name
	}
	return fmt.Sprintf("format_id(%d)", int(f))
}

// Extension is the container the format arrives in. Files are named before
// anything transfers, so this has to be decidable from the format alone.
func (f Format) Extension() string {
	if f == FormatMP3 {
		return "mp3"
	}
	return "flac"
}

// Bitrate is the constant bitrate of a lossy format, in kbps, and zero for lossless.
func (f Format) Bitrate() int {
	if f == FormatMP3 {
		return 320
	}
	return 0
}

// fallbacks lists the formats to try, best first, when f is refused.
//
// Only formats sharing f's container are offered. Dropping from FLAC to MP3 would change
// the extension, and the file has already been named by then.
func (f Format) fallbacks() []Format {
	if f == FormatMP3 {
		return []Format{FormatMP3}
	}
	lossless := []Format{FormatHiRes, FormatFLAC96, FormatFLAC}
	for i, candidate := range lossless {
		if candidate == f {
			return lossless[i:]
		}
	}
	return []Format{FormatFLAC}
}

// Clamp lowers a preferred format to what the album actually offers.
//
// Qobuz answers a request for 24-bit audio of a 16-bit master with a 16-bit file, and
// refuses hi-res outright to an account that may not stream it. Deciding it here keeps the
// folder named for the quality equal to what the files in it carry.
func Clamp(preferred Format, album *Album) Format {
	if preferred == FormatMP3 || album == nil {
		return preferred
	}
	if !album.HiResStreamable || album.MaxBitDepth < 24 {
		return FormatFLAC
	}
	if preferred == FormatHiRes && album.MaxSampleRate <= 96 {
		return FormatFLAC96
	}
	return preferred
}
