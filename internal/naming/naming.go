// Package naming turns album and track metadata into file and folder names, through the
// templates the user configures.
package naming

import (
	"fmt"
	"strings"
	"unicode"
)

// Defaults are the templates used when the configuration names none.
const (
	DefaultFolder = "{artist} - {album} ({year}) [{bit_depth}B-{sampling_rate}kHz]"
	DefaultTrack  = "{tracknumber}. {tracktitle}"
)

// Variables are the values a template may name, keyed without braces.
type Variables map[string]string

// Expand replaces every {name} in the template with its variable and makes the result
// safe as one path component. A name the variables do not carry expands to nothing, so a
// template written for an album still works on a track that lacks a field.
//
// Only the whole result is sanitised, not each variable: "Slash/Band" becomes "Slash-Band" either
// way, and a template that contains a slash on purpose gets the same treatment, since a
// name here is always one component.
func Expand(template string, vars Variables) string {
	var b strings.Builder
	rest := template
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			b.WriteString(rest)
			break
		}
		b.WriteString(rest[:open])
		name := rest[open+1 : open+closing]
		b.WriteString(vars[strings.ToLower(strings.TrimSpace(name))])
		rest = rest[open+closing+1:]
	}
	return Sanitize(b.String())
}

// Variables lists the names a template may use, for the help text.
func Names() []string {
	return []string{
		"artist", "albumartist", "album", "version", "year", "date", "label", "genre",
		"bit_depth", "sampling_rate", "quality", "id", "upc",
		"tracknumber", "tracktitle", "trackartist", "composer", "discnumber", "disctotal", "tracktotal",
		"playlist", "playlistindex",
	}
}

// maxComponent bounds one name in bytes. Most filesystems refuse 256; staying well under
// leaves room for the extension and for the temporary suffix a download uses.
const maxComponent = 200

// Sanitize makes s safe as one path component on every filesystem this runs on: the
// separators and the characters Windows refuses are replaced, control characters dropped,
// trailing dots and spaces trimmed, the length bounded. An empty result becomes "Unknown"
// rather than nothing, since a nameless file would not be a file.
func Sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\':
			b.WriteRune('-')
		case r == ':':
			b.WriteRune('-')
		case r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimRight(out, ". ")
	out = strings.TrimLeft(out, ".")
	if len(out) > maxComponent {
		out = truncate(out, maxComponent)
	}
	if out == "" {
		return "Unknown"
	}
	return out
}

// truncate cuts at a rune boundary so a multi-byte character is never split.
func truncate(s string, n int) string {
	for n > 0 && n < len(s) && !isRuneStart(s[n]) {
		n--
	}
	return strings.TrimRight(s[:n], ". ")
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// Number renders a track number padded to the width the album needs: two digits for anything
// under a hundred tracks, more for the rare box set that runs longer.
func Number(position, total int) string {
	width := 2
	for limit := 100; total >= limit; limit *= 10 {
		width++
	}
	return fmt.Sprintf("%0*d", width, position)
}
