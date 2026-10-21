// Package attachmentname keeps attachment risk checks and downloads on the same filename.
package attachmentname

import (
	"io"
	"mime"
	"strings"
	"unicode"

	"golang.org/x/net/html/charset"
)

// Normalize removes characters that download clients can discard or interpret as
// path separators. It also decodes legacy encoded words. Apply at ingest and at
// use time, so attachments already in the database receive the same protection.
func Normalize(name string) string {
	decoder := mime.WordDecoder{CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
		return charset.NewReaderLabel(label, input)
	}}
	for {
		previous := name
		// Finish decoding before stripping: a nested encoded word may contain
		// a slash as base64 data rather than as a filename separator.
		for {
			decoded, err := decoder.DecodeHeader(name)
			if err != nil || decoded == name {
				break
			}
			name = decoded
		}
		name = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '"' || r == '\\' || r == '/' {
				return -1
			}
			return r
		}, name)
		// Removing characters or decoding a word can expose another encoded word.
		if name == previous {
			return name
		}
	}
}
