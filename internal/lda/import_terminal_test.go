package lda

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportTerminalDiagnostics(t *testing.T) {
	// Do not run in parallel: the standard logger is process-global.
	const payload = "\x1b]52;c;evil\a\u009b\u202e\xff"
	for _, format := range []string{"mbox", "mbx", "maildir"} {
		t.Run(format, func(t *testing.T) {
			db := openImportTestDB(t)
			path := filepath.Join(t.TempDir(), "archive"+payload)
			message := "From: sender@example.org\r\nX" + payload + "\r\n\r\nbody\r\n"
			switch format {
			case "mbox":
				require.NoError(t, os.WriteFile(path, []byte("From sender@example.org Mon Jan  1 10:00:00 2024\n"+message), 0600))
			case "mbx":
				header := make([]byte, 2048)
				copy(header, "*mbx*\r\n")
				content := append(header, []byte(fmt.Sprintf(" 1-Jan-2024 10:00:00 +0000,%d;000000000000-00000001\r\n%s", len(message), message))...)
				require.NoError(t, os.WriteFile(path, content, 0600))
			case "maildir":
				require.NoError(t, os.MkdirAll(filepath.Join(path, "new"), 0700))
				require.NoError(t, os.MkdirAll(filepath.Join(path, "cur"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(path, "new", "message"+payload), []byte(message), 0600))
			}
			var output bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&output)
			defer log.SetOutput(previous)
			var imported, skipped int
			var err error
			switch format {
			case "mbox":
				imported, skipped, err = importMbox(path, 1, db)
			case "mbx":
				imported, skipped, err = importMbx(path, 1, db)
			case "maildir":
				imported, skipped, err = importMaildir(path, 1, db)
			}
			require.NoError(t, err)
			assert.Equal(t, 0, imported)
			assert.Equal(t, 1, skipped)
			assert.Contains(t, output.String(), "parse")
			assert.Contains(t, output.String(), "\\u001b]52;c;evil\\u0007\\u009b\\u202e\\xff")
			for _, unsafe := range []string{"\x1b", "\a", "\u009b", "\u202e", "\xff"} {
				assert.NotContains(t, output.String(), unsafe)
			}
		})
	}
}
