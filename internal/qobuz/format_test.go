package qobuz

import "testing"

func TestParseFormat(t *testing.T) {
	for _, tc := range []struct {
		quality string
		want    Format
		wantErr bool
	}{
		{quality: "", want: DefaultFormat},
		{quality: "27", want: FormatHiRes},
		{quality: "flac-24-192", want: FormatHiRes},
		{quality: "FLAC-24-96", want: FormatFLAC96},
		{quality: " 7 ", want: FormatFLAC96},
		{quality: "flac-16", want: FormatFLAC},
		{quality: "mp3-320", want: FormatMP3},
		{quality: "flac", wantErr: true},
		{quality: "9", wantErr: true},
		{quality: "best", wantErr: true},
	} {
		got, err := ParseFormat(tc.quality)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseFormat(%q) error = %v, wantErr %v", tc.quality, err, tc.wantErr)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("ParseFormat(%q) = %s, want %s", tc.quality, got, tc.want)
		}
	}
}

func TestFormatContainer(t *testing.T) {
	if FormatMP3.Extension() != "mp3" || FormatMP3.Bitrate() != 320 {
		t.Error("MP3 should be an mp3 at 320 kbps")
	}
	for _, format := range []Format{FormatFLAC, FormatFLAC96, FormatHiRes} {
		if format.Extension() != "flac" || format.Bitrate() != 0 {
			t.Errorf("%s should be a lossless flac", format)
		}
	}
	if Format(99).Valid() {
		t.Error("an unknown format id must not be valid: the manifest needs an extension")
	}
}

// A fallback may lower the quality but never change the container: the manifest naming the
// file has already been written.
func TestFormatFallbacksStayInOneContainer(t *testing.T) {
	for _, tc := range []struct {
		format Format
		want   []Format
	}{
		{FormatHiRes, []Format{FormatHiRes, FormatFLAC96, FormatFLAC}},
		{FormatFLAC96, []Format{FormatFLAC96, FormatFLAC}},
		{FormatFLAC, []Format{FormatFLAC}},
		{FormatMP3, []Format{FormatMP3}},
	} {
		got := tc.format.fallbacks()
		if len(got) != len(tc.want) {
			t.Errorf("%s fallbacks = %v, want %v", tc.format, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s fallbacks = %v, want %v", tc.format, got, tc.want)
				break
			}
			if got[i].Extension() != tc.format.Extension() {
				t.Errorf("%s fell back to %s, another container", tc.format, got[i])
			}
		}
	}
}

func TestClampToWhatTheAlbumOffers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		preferred Format
		album     Album
		want      Format
	}{
		{
			name:      "hi-res master gives hi-res",
			preferred: FormatHiRes,
			album:     Album{MaxBitDepth: 24, MaxSampleRate: 192, HiResStreamable: true},
			want:      FormatHiRes,
		},
		{
			name:      "24/96 master cannot give 192",
			preferred: FormatHiRes,
			album:     Album{MaxBitDepth: 24, MaxSampleRate: 96, HiResStreamable: true},
			want:      FormatFLAC96,
		},
		{
			name:      "16-bit master gives 16-bit",
			preferred: FormatHiRes,
			album:     Album{MaxBitDepth: 16, MaxSampleRate: 44.1, HiResStreamable: false},
			want:      FormatFLAC,
		},
		{
			name:      "hi-res the account may not stream",
			preferred: FormatFLAC96,
			album:     Album{MaxBitDepth: 24, MaxSampleRate: 96, HiResStreamable: false},
			want:      FormatFLAC,
		},
		{
			name:      "lossy is never upgraded",
			preferred: FormatMP3,
			album:     Album{MaxBitDepth: 24, MaxSampleRate: 192, HiResStreamable: true},
			want:      FormatMP3,
		},
		{
			name:      "asking for less than the album has changes nothing",
			preferred: FormatFLAC,
			album:     Album{MaxBitDepth: 24, MaxSampleRate: 192, HiResStreamable: true},
			want:      FormatFLAC,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clamp(tc.preferred, &tc.album); got != tc.want {
				t.Errorf("Clamp(%s) = %s, want %s", tc.preferred, got, tc.want)
			}
		})
	}
}
