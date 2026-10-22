package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalResponses(t *testing.T) {
	attack := "Hello\r\n\x1b]52;c;Y3VybCBldmlsfHNo\a\x1b[2J\x1b[H\x1b]8;;https://evil.example/\aBank\x1b]8;;\a\u009b2J\u009d52;c;x\u202eexe\u2066x\u2069\x7f\b\rOverwrite\tå"
	safe := "Hello\r\n\\u001b]52;c;Y3VybCBldmlsfHNo\\u0007\\u001b[2J\\u001b[H\\u001b]8;;https://evil.example/\\u0007Bank\\u001b]8;;\\u0007\\u009b2J\\u009d52;c;x\\u202eexe\\u2066x\\u2069\\u007f\\u0008\\u000dOverwrite\tå"
	for _, command := range [][]string{
		{"messages", "text", "9"}, {"messages", "headers", "9"},
		{"messages", "raw", "9"}, {"messages", "body", "9"},
		{"attachments", "get", "9"}, {"messages", "get", "9"},
		{"messages", "list", "1"}, {"messages", "search", "-folder", "1", "-q", "hello"},
		{"folders", "list"},
	} {
		t.Run(strings.Join(command, "_"), func(t *testing.T) {
			isText := command[1] == "text"
			isJSON := command[0] == "folders" || command[0] == "messages" &&
				(command[1] == "get" || command[1] == "list" || command[1] == "search")
			body, want := attack, safe
			if isText || isJSON {
				encoded, err := json.Marshal(map[string]string{"body_text": attack})
				require.NoError(t, err)
				body = string(encoded)
				if isJSON {
					want = strings.NewReplacer(
						"\u009b", "\\u009b", "\u009d", "\\u009d", "\u202e", "\\u202e",
						"\u2066", "\\u2066", "\u2069", "\\u2069", "\x7f", "\\u007f",
					).Replace(body)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, body)
			}))
			defer server.Close()
			for _, terminal := range []bool{false, true} {
				var output bytes.Buffer
				args := append([]string{"-url", server.URL}, command...)
				require.NoError(t, runWithTerminal(args, nil, &output, terminal))
				expected := body
				if isText {
					expected = attack
				}
				if terminal {
					expected = want
				}
				assert.Equal(t, expected, output.String())
				if isJSON {
					var decoded map[string]string
					require.NoError(t, json.Unmarshal(output.Bytes(), &decoded))
					assert.Equal(t, attack, decoded["body_text"])
				}
			}
		})
	}
}

func TestTerminalControlsAndInvalidUTF8(t *testing.T) {
	var input, expected strings.Builder
	for r := rune(0); r <= 0x9f; r++ {
		input.WriteRune(r)
		switch {
		case r == '\t' || r == '\n' || r >= 0x20 && r < 0x7f:
			expected.WriteRune(r)
		default:
			// JSON provides exactly the Unicode escape spelling used for controls.
			// C1 and DEL must be escaped explicitly.
			const hex = "0123456789abcdef"
			expected.WriteString("\\u00")
			expected.WriteByte(hex[r>>4])
			expected.WriteByte(hex[r&15])
		}
	}
	input.WriteString("\xff\x9b\xc2\x1b\r\n\r")
	expected.WriteString("\\xff\\x9b\\xc2\\u001b\r\n\\u000d")
	var output bytes.Buffer
	require.NoError(t, copyTerminal(&output, strings.NewReader(input.String())))
	assert.Equal(t, expected.String(), output.String())
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestTerminalWriteError(t *testing.T) {
	require.ErrorContains(t, copyTerminal(failedWriter{}, strings.NewReader("hello")), "write failed")
}

func TestTerminalReadErrorFlushesOutput(t *testing.T) {
	readErr := errors.New("download interrupted")
	reader := io.MultiReader(strings.NewReader("hello\x1b"), iotest.ErrReader(readErr))
	var output bytes.Buffer
	require.ErrorIs(t, copyTerminal(&output, reader), readErr)
	assert.Equal(t, "hello\\u001b", output.String())
	require.ErrorContains(t, copyTerminal(failedWriter{}, io.MultiReader(strings.NewReader("hello"), iotest.ErrReader(readErr))), "write failed")
}

func TestCLIErrorOutput(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		var output bytes.Buffer
		require.NoError(t, writeCLIError(&output, errors.New("HTTP 403: \x1b]52;c;x\a\u202e"), terminal))
		expected := "mymail-cli: HTTP 403: \x1b]52;c;x\a\u202e\n"
		if terminal {
			expected = "mymail-cli: HTTP 403: \\u001b]52;c;x\\u0007\\u202e\n"
		}
		assert.Equal(t, expected, output.String())
	}
}
