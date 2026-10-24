// Package termsafe escapes untrusted text for terminal display.
package termsafe

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Copy makes untrusted response bytes safe to display without buffering
// downloads in memory. Unicode escapes also preserve the values of JSON strings.
func Copy(dst io.Writer, src io.Reader) error {
	reader := bufio.NewReader(src)
	writer := bufio.NewWriter(dst)
	for {
		r, size, err := reader.ReadRune()
		if err == io.EOF {
			return writer.Flush()
		}
		if err != nil {
			return errors.Join(err, writer.Flush())
		}
		switch {
		case r == utf8.RuneError && size == 1:
			// Recover the original invalid byte, which may itself be an 8-bit CSI.
			if err := reader.UnreadRune(); err != nil {
				return err
			}
			b, err := reader.ReadByte()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(writer, "\\x%02x", b)
			if err != nil {
				return err
			}
		case r == '\r':
			// A lone CR can overwrite visible text; only preserve CRLF.
			next, _ := reader.Peek(1)
			if len(next) == 1 && next[0] == '\n' {
				_, err = writer.WriteRune(r)
			} else {
				_, err = writer.WriteString("\\u000d")
			}
		case r < 0x20 && r != '\n' && r != '\t',
			r >= 0x7f && r <= 0x9f,
			r == 0x061c || r == 0x200e || r == 0x200f,
			r >= 0x202a && r <= 0x202e,
			r >= 0x2066 && r <= 0x2069:
			_, err = fmt.Fprintf(writer, "\\u%04x", r)
		default:
			_, err = writer.WriteRune(r)
		}
		if err != nil {
			return err
		}
	}
}

// String returns text with terminal controls visibly escaped.
func String(text string) string {
	var output strings.Builder
	_ = Copy(&output, strings.NewReader(text))
	return output.String()
}
