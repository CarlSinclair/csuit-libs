// Package filesig identifies an uploaded file by its leading bytes instead of
// trusting the file name or the client-supplied content type.
package filesig

import "bytes"

type Kind string

const (
	Unknown Kind = ""
	PNG     Kind = "png"
	JPEG    Kind = "jpeg"
	GIF     Kind = "gif"
	WebP    Kind = "webp"
	PDF     Kind = "pdf"
	MP4     Kind = "mp4"
	WebM    Kind = "webm"
	AVI     Kind = "avi"
)

// HeadSize is how many leading bytes Detect needs.
const HeadSize = 16

// Detect returns the kind of file whose first bytes are head, or Unknown.
func Detect(head []byte) Kind {
	switch {
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return PNG
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return JPEG
	case bytes.HasPrefix(head, []byte("GIF87a")), bytes.HasPrefix(head, []byte("GIF89a")):
		return GIF
	case bytes.HasPrefix(head, []byte("%PDF-")):
		return PDF
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("WEBP")):
		return WebP
	case len(head) >= 12 && bytes.HasPrefix(head, []byte("RIFF")) && bytes.Equal(head[8:12], []byte("AVI ")):
		return AVI
	case len(head) >= 8 && bytes.Equal(head[4:8], []byte("ftyp")):
		return MP4
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return WebM
	}
	return Unknown
}

// IsVideo reports whether k is a container a video upload may use (MP4 and
// QuickTime share the ftyp box; WebM covers Matroska).
func (k Kind) IsVideo() bool { return k == MP4 || k == WebM || k == AVI }

// IsImage reports whether k is a raster image kind.
func (k Kind) IsImage() bool { return k == PNG || k == JPEG || k == GIF || k == WebP }
