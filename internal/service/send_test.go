package service

import (
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMIMEMessageQuotesCommaInDisplayNames(t *testing.T) {
	raw, _, _, err := BuildMIMEMessage(SendFields{
		FromName:    "Sender, Sam",
		FromAddr:    "sam@example.com",
		ToAddr:      `"Svensson, Anna" <asvensson@example.com>`,
		CcAddr:      `"Doe, Jane" <jane@example.com>`,
		BccAddr:     `"Roe, Alex" <alex@example.com>`,
		ReplyToAddr: `"Sender, Sam" <sam@example.com>`,
		BodyText:    "hello",
	}, nil)
	require.NoError(t, err)

	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	require.NoError(t, err)
	for header, want := range map[string]string{
		"From":     "sam@example.com",
		"To":       "asvensson@example.com",
		"Cc":       "jane@example.com",
		"Bcc":      "alex@example.com",
		"Reply-To": "sam@example.com",
	} {
		addrs, err := mail.ParseAddressList(message.Header.Get(header))
		require.NoError(t, err, header)
		require.Len(t, addrs, 1, header)
		require.Equal(t, want, addrs[0].Address, header)
		require.Contains(t, addrs[0].Name, ",", header)
	}
	require.Contains(t, message.Header.Get("To"), `"Svensson, Anna"`)
}

func TestBuildMIMEMessageRejectsMalformedAddressList(t *testing.T) {
	_, _, _, err := BuildMIMEMessage(SendFields{
		FromAddr: "sender@example.com",
		ToAddr:   "Svensson, Anna <asvensson@example.com>",
		BodyText: "hello",
	}, nil)
	require.ErrorContains(t, err, "invalid To address list")
}
