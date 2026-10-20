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
//
// The result is either a list that net/mail parses back to the same
// addresses, or — when the header is not a valid address list — text that
// net/mail rejects. It never substitutes anything else for an address that did
// not parse: every consumer re-parses the stored string, and a display name
// stored in place of a malformed addr-spec (`"ceo@bank.com" <x..y@evil>`)
// would be read back as a forged, well-formed sender.
func DecodeAddressHeader(s string) string {
	if s == "" {
		return ""
	}

	dec := NewWordDecoder()

	// Encoded words are decoded only where RFC 2047 allows them, in display
	// names — never in an addr-spec, which they could otherwise forge.
	parser := mail.AddressParser{WordDecoder: dec}
	if addrs, err := parser.ParseList(s); err == nil {
		parts := make([]string, 0, len(addrs))
		for _, a := range addrs {
			name := a.Name
			// A quoted encoded word is not decoded by the parser, but senders
			// produce them anyway.
			if decodedName, err := dec.DecodeHeader(name); err == nil {
				name = decodedName
			}
			parts = append(parts, formatAddress(name, a.Address))
		}
		return strings.Join(parts, ", ")
	}

	// Not an address list. Keep the text for display, decoded if that cannot
	// make it parse — an encoded word must not turn into an address.
	text := strings.TrimSpace(s)
	if decoded, err := dec.DecodeHeader(text); err == nil && !parsesAsAddressList(decoded) {
		return strings.TrimSpace(decoded)
	}
	if !parsesAsAddressList(text) {
		return text
	}
	return ""
}

func parsesAsAddressList(s string) bool {
	_, err := mail.ParseAddressList(s)
	return err == nil
}

// nameNeedsQuoting holds what a display name may not contain unquoted. It is
// the same set as NAME_NEEDS_QUOTING in web/ts/util/address.ts.
const nameNeedsQuoting = `"(),.:;<>@[]\`

// formatAddress renders one address the way formatAddress in
// web/ts/util/address.ts does: the display name stays decoded (not RFC 2047
// encoded, unlike mail.Address.String) and is quoted when it contains a
// special, so that `"boss@company.org" <evil@example.com>` and
// `"Doe, John" <john@example.com>` still parse as one address with that name.
// A `"` or `\` in the name is dropped rather than escaped, since the web UI's
// address splitting does not understand escapes.
func formatAddress(name, address string) string {
	name = strings.TrimSpace(strings.NewReplacer(`"`, "", `\`, "").Replace(name))
	if name == "" {
		return address
	}
	if strings.ContainsAny(name, nameNeedsQuoting) {
		return `"` + name + `" <` + address + ">"
	}
	return name + " <" + address + ">"
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
