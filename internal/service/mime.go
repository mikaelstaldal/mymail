package service

import (
	"bytes"
	"io"
	"mime"
	"net/mail"
	"strings"

	"golang.org/x/net/html/charset"
)

// NewWordDecoder returns a mime.WordDecoder with a CharsetReader that supports
// common charsets via golang.org/x/net/html/charset.
func NewWordDecoder() *mime.WordDecoder {
	return &mime.WordDecoder{
		CharsetReader: func(label string, input io.Reader) (io.Reader, error) {
			return charset.NewReaderLabel(label, input)
		},
	}
}

// ParseAddressList parses a comma-separated list of addresses, with support for
// additional charsets in RFC 2047 encoded words.
func ParseAddressList(s string) ([]*mail.Address, error) {
	addrs, err := mail.ParseAddressList(s)
	if err == nil {
		return addrs, nil
	}

	// Try to decode first using our expanded decoder
	dec := NewWordDecoder()
	if decoded, err2 := dec.DecodeHeader(s); err2 == nil && decoded != s {
		if addrs2, err3 := mail.ParseAddressList(decoded); err3 == nil {
			return addrs2, nil
		}
	}

	return nil, err
}

// ParseAddress parses a single address, with support for additional charsets
// in RFC 2047 encoded words.
func ParseAddress(s string) (*mail.Address, error) {
	addr, err := mail.ParseAddress(s)
	if err == nil {
		return addr, nil
	}

	// Try to decode first using our expanded decoder
	dec := NewWordDecoder()
	if decoded, err2 := dec.DecodeHeader(s); err2 == nil && decoded != s {
		if addr2, err3 := mail.ParseAddress(decoded); err3 == nil {
			return addr2, nil
		}
	}

	return nil, err
}

// DecodeAddressHeader decodes RFC 2047 encoded words in an address header,
// attempting to preserve address structure.
func DecodeAddressHeader(s string) string {
	if s == "" {
		return ""
	}

	dec := NewWordDecoder()

	// Try standard parsing first
	if addrs, err := mail.ParseAddressList(s); err == nil {
		parts := make([]string, 0, len(addrs))
		for _, a := range addrs {
			name := a.Name
			if decodedName, err := dec.DecodeHeader(name); err == nil {
				name = decodedName
			}
			if name != "" {
				parts = append(parts, name+" <"+a.Address+">")
			} else {
				parts = append(parts, a.Address)
			}
		}
		return strings.Join(parts, ", ")
	}

	// Fallback: decode everything and try to parse
	decoded := s
	if d, err := dec.DecodeHeader(s); err == nil {
		decoded = d
		if addrs, err := mail.ParseAddressList(decoded); err == nil {
			parts := make([]string, 0, len(addrs))
			for _, a := range addrs {
				if a.Name != "" {
					parts = append(parts, a.Name+" <"+a.Address+">")
				} else {
					parts = append(parts, a.Address)
				}
			}
			return strings.Join(parts, ", ")
		}
	}

	// The addr-spec itself is malformed (e.g. an unescaped space), so it
	// can't be salvaged. Still show the sender's display name instead of
	// dropping the header entirely.
	if name := displayNameBeforeAngleAddr(decoded); name != "" {
		if decodedName, err := dec.DecodeHeader(name); err == nil {
			name = decodedName
		}
		return name
	}

	return ""
}

// displayNameBeforeAngleAddr returns the trimmed, unquoted text preceding the
// final "<...>" addr-spec in s, or "" if s has no such bracketed address.
func displayNameBeforeAngleAddr(s string) string {
	idx := strings.LastIndex(s, "<")
	if idx < 0 {
		return ""
	}
	name := strings.TrimSpace(s[:idx])
	return strings.TrimSpace(strings.Trim(name, `"`))
}

// DecodeCharset decodes data using the charset specified in the Content-Type header.
func DecodeCharset(data []byte, contentType string) string {
	r, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		return strings.ToValidUTF8(string(data), "")
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		return strings.ToValidUTF8(string(data), "")
	}
	return strings.ToValidUTF8(string(decoded), "")
}
