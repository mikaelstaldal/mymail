package lda

import (
	"context"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSocketRejectsOverLimitMIME(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lda.sock")
	ln, err := BindSocket(path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ServeSocket(ctx, ln, nil)

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
	_, err = conn.Write(nestedMIME(maxMIMEDepth+1, "mixed"))
	require.NoError(t, err)
	require.NoError(t, conn.CloseWrite())
	response, err := io.ReadAll(conn)
	require.NoError(t, err)
	assert.Equal(t, "parse_error", string(response))
}
