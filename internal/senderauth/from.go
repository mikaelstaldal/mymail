package senderauth

import (
	"mime"
	"net/mail"
)

// SingleFrom returns one parsed mailbox only when exactly one From field exists.
// A display-string fallback must never authorize sender-based routing or blocking.
func SingleFrom(h mail.Header, decoder *mime.WordDecoder) string {
	fields := Fields(h, "From")
	if len(fields) != 1 {
		return ""
	}
	parser := mail.AddressParser{WordDecoder: decoder}
	addresses, err := parser.ParseList(fields[0])
	if err != nil || len(addresses) != 1 {
		return ""
	}
	return addresses[0].Address
}
