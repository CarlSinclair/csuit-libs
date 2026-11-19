package filesig

import "testing"

func TestDetect(t *testing.T) {
	cases := map[Kind][]byte{
		PNG:  {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0},
		JPEG: {0xFF, 0xD8, 0xFF, 0xE0},
		GIF:  []byte("GIF89a...."),
		PDF:  []byte("%PDF-1.7"),
		WebP: []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		AVI:  []byte("RIFF\x00\x00\x00\x00AVI LIST"),
		MP4:  []byte("\x00\x00\x00\x18ftypmp42"),
		WebM: {0x1A, 0x45, 0xDF, 0xA3, 0x9F},
	}
	for want, head := range cases {
		if got := Detect(head); got != want {
			t.Errorf("Detect(%q) = %q, want %q", head, got, want)
		}
	}
	if Detect([]byte("<html>not a video")) != Unknown {
		t.Error("html must be Unknown")
	}
	if !MP4.IsVideo() || PDF.IsVideo() || !PNG.IsImage() {
		t.Error("kind helpers wrong")
	}
}
