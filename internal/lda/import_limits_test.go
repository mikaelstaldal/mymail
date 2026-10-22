package lda

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Stream fixture data to disk so the test itself never holds the oversized body.
func TestImportOversizedBounded(t *testing.T) {
	for _, format := range []string{"maildir", "mbox", "mbx"} {
		t.Run(format, func(t *testing.T) {
			db := openImportTestDB(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "archive")
			if format == "maildir" {
				require.NoError(t, os.MkdirAll(filepath.Join(path, "new"), 0700))
				require.NoError(t, os.MkdirAll(filepath.Join(path, "cur"), 0700))
			}
			filename := path
			if format == "maildir" {
				filename = filepath.Join(path, "new", "001")
			}
			f, err := os.Create(filename)
			require.NoError(t, err)
			write := func(s string) { _, err := f.WriteString(s); require.NoError(t, err) }
			const size = 4 * MaxMessageBytes
			const separator = "From sender@example.org Mon Jan  1 10:00:00 2024\n"
			small := "From: sender@example.org\r\nMessage-ID: <after-limit@example.org>\r\n\r\nSmall body\r\n"
			if format == "mbx" {
				header := make([]byte, 2048)
				copy(header, "*mbx*\r\n")
				_, err = f.Write(header)
				require.NoError(t, err)
				write(fmt.Sprintf(" 1-Jan-2024 10:00:00 +0000,%d;000000000000-00000001\r\n", size))
			}
			if format == "mbox" {
				write(separator)
			}
			// A single huge line also exercises the separator scan's bounded fragments.
			chunk := strings.Repeat("x", 1024*1024)
			for n := 0; n < size/len(chunk); n++ {
				write(chunk)
			}
			if format == "mbox" {
				write("\n" + separator + small)
			}
			if format == "mbx" {
				write(fmt.Sprintf(" 1-Jan-2024 10:00:00 +0000,%d;000000000000-00000002\r\n", len(small)))
				write(small)
			}
			require.NoError(t, f.Close())
			if format == "maildir" {
				require.NoError(t, os.WriteFile(filepath.Join(path, "new", "002"), []byte(small), 0600))
			}
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			var imported, skipped int
			switch format {
			case "maildir":
				imported, skipped, err = importMaildir(path, 1, db)
			case "mbox":
				imported, skipped, err = importMbox(path, 1, db)
			case "mbx":
				imported, skipped, err = importMbx(path, 1, db)
			}
			runtime.ReadMemStats(&after)
			require.NoError(t, err)
			assert.Equal(t, 1, imported)
			assert.Equal(t, 1, skipped)
			// Total allocation is a conservative, deterministic bound on peak live heap.
			// It catches full-body reads while allowing ReadAll's bounded buffer growth.
			assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(400*1024*1024))
		})
	}
}

// A truncated oversized body is an input error, while preceding messages still commit.
func TestImportMbxTruncatedOversized(t *testing.T) {
	db := openImportTestDB(t)
	path := writeMbxFile(t, []mbxTestMsg{{raw: "From: sender@example.org\r\nMessage-ID: <before-limit@example.org>\r\n\r\nBody\r\n"}})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = fmt.Fprintf(f, " 1-Jan-2024 10:00:00 +0000,%d;000000000000-00000002\r\nshort", MaxMessageBytes+1)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	imported, skipped, err := importMbx(path, 1, db)
	require.Error(t, err)
	assert.Equal(t, 1, imported)
	assert.Zero(t, skipped)
}

func TestImportMbxBoundsMalformedHeader(t *testing.T) {
	db := openImportTestDB(t)
	path := writeMbxFile(t, nil)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.WriteString(strings.Repeat("x", 1024*1024))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	imported, skipped, err := importMbx(path, 1, db)
	runtime.ReadMemStats(&after)
	require.Error(t, err)
	assert.Zero(t, imported)
	assert.Zero(t, skipped)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(512*1024))
}
