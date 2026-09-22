package service

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildMIMEMessageKeepsASCIILineBreaksReadable(t *testing.T) {
	text := "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor\n" +
		"incididunt ut labore et dolore magna aliqua."
	raw, _, _, err := BuildMIMEMessage(SendFields{
		FromAddr: "sender@example.com", ToAddr: "reader@example.com",
		BodyText: text, BodyHTML: `<p>Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.</p>`,
	}, nil)
	require.NoError(t, err)
	message, err := mail.ReadMessage(strings.NewReader(string(raw)))
	require.NoError(t, err)
	_, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	require.NoError(t, err)
	reader := multipart.NewReader(message.Body, params["boundary"])
	part, err := reader.NextPart()
	require.NoError(t, err)
	require.Equal(t, "7bit", part.Header.Get("Content-Transfer-Encoding"))
	body, err := io.ReadAll(part)
	require.NoError(t, err)
	require.Equal(t, strings.ReplaceAll(text, "\n", "\r\n"), string(body))
}

func TestEncodePlainTextFallsBackWhenSevenBitWouldChangeContent(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		cte  string
	}{
		{"non-ASCII", "café", "quoted-printable"},
		{"trailing space", "one ", "quoted-printable"},
		{"long line", strings.Repeat("x", 999), "quoted-printable"},
		{"CRLF", "one\r\ntwo", "7bit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cte, body := encodePlainText(tc.text)
			require.Equal(t, tc.cte, cte)
			if cte == "7bit" {
				require.Equal(t, tc.text, string(body))
			}
		})
	}
}

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
