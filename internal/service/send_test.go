package service

import (
	"bytes"
	"fmt"
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

func TestOutgoingHeadersFoldWithoutChangingValues(t *testing.T) {
	for _, subject := range []string{strings.Repeat("räksmörgås ", 90), strings.Repeat("x", 998)} {
		var refs, addresses []string
		for i := 0; i < 400; i++ {
			refs = append(refs, fmt.Sprintf("message-%d@example.com", i))
		}
		for i := 0; i < 80; i++ {
			addresses = append(addresses, fmt.Sprintf("\"Person, %d\" <person%d@example.com>", i, i))
		}
		fields := SendFields{
			FromAddr: "sender@example.com", FromName: "Sender, Sam",
			ToAddr: strings.Join(addresses, ", "), CcAddr: strings.Join(addresses, ", "),
			BccAddr: strings.Join(addresses, ", "), ReplyToAddr: strings.Join(addresses, ", "),
			Subject: subject, References: refs, InReplyTo: refs[len(refs)-1], BodyText: "hello",
		}
		raw, _, _, err := BuildMIMEMessage(fields, nil)
		require.NoError(t, err)
		headers := strings.SplitN(string(raw), "\r\n\r\n", 2)[0]
		for _, line := range strings.Split(headers, "\r\n") {
			require.LessOrEqual(t, len(line), 998, line)
		}
		message, err := mail.ReadMessage(bytes.NewReader(raw))
		require.NoError(t, err)
		decoded, err := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
		require.NoError(t, err)
		require.Equal(t, subject, decoded)
		require.Equal(t, "<"+strings.Join(refs, "> <")+">", message.Header.Get("References"))
		require.Equal(t, "<"+fields.InReplyTo+">", message.Header.Get("In-Reply-To"))
		for _, header := range []string{"To", "Cc", "Bcc", "Reply-To"} {
			got, err := mail.ParseAddressList(message.Header.Get(header))
			require.NoError(t, err)
			want, err := mail.ParseAddressList(fields.ToAddr)
			require.NoError(t, err)
			require.Equal(t, want, got)
		}
	}
}

func TestOutgoingHeadersRejectIndivisibleOversizedTokens(t *testing.T) {
	for _, field := range []string{"In-Reply-To", "References", "From"} {
		t.Run(field, func(t *testing.T) {
			fields := SendFields{FromAddr: "sender@example.com", ToAddr: "reader@example.com"}
			switch field {
			case "In-Reply-To":
				fields.InReplyTo = strings.Repeat("x", 998)
			case "References":
				fields.References = []string{strings.Repeat("x", 998)}
			case "From":
				fields.FromName = strings.Repeat("x", 1000)
			}
			raw, _, _, err := BuildMIMEMessage(fields, nil)
			require.ErrorContains(t, err, field+" header")
			require.Nil(t, raw)
		})
	}
}

func TestHeaderFoldingPreservesWhitespaceWithoutEmptyContinuation(t *testing.T) {
	for _, value := range []string{
		strings.Repeat("word ", 30),
		"quoted \"name with spaces and an escaped \\\" quote\" <someone@example.com>",
	} {
		var out bytes.Buffer
		require.NoError(t, writeFoldedHeader(&out, "Test", value))
		for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\r\n"), "\r\n")[1:] {
			require.NotEmpty(t, strings.TrimSpace(line))
			require.Contains(t, " \t", line[:1])
		}
		unfolded := strings.ReplaceAll(strings.TrimSuffix(out.String(), "\r\n"), "\r\n", "")
		require.Equal(t, "Test: "+value, unfolded)
	}
}

func TestShortASCIISubjectWithSyntaxCharactersStaysWithinTarget(t *testing.T) {
	subject := "<" + strings.Repeat("x", 76)
	raw, _, _, err := BuildMIMEMessage(SendFields{
		FromAddr: "sender@example.com", ToAddr: "reader@example.com", Subject: subject,
	}, nil)
	require.NoError(t, err)
	for _, line := range strings.Split(strings.SplitN(string(raw), "\r\n\r\n", 2)[0], "\r\n") {
		require.LessOrEqual(t, len(line), 78)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	require.NoError(t, err)
	got, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	require.NoError(t, err)
	require.Equal(t, subject, got)
}
