package lda

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
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

// zeroReader supplies large messages without allocating their bodies in tests.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestSocketOversizeIsPermanentFailure(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mymail-lda")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/lda")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)

	path := filepath.Join(t.TempDir(), "lda.sock")
	ln, err := BindSocket(path)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ServeSocket(ctx, ln, nil)

	for _, extra := range []int64{1, 1000, 8 << 20} {
		t.Run(fmt.Sprintf("extra_%d", extra), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-lda-socket", path)
			cmd.Stdin = io.LimitReader(zeroReader{}, MaxMessageBytes+extra)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "%s", output)
			assert.Equal(t, 1, exit.ExitCode(), "%s", output)
		})
	}
}

func TestSocketDrainsOversizedMessage(t *testing.T) {
	for _, extra := range []int64{1000, 8 << 20} {
		t.Run(fmt.Sprintf("extra_%d", extra), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lda.sock")
			ln, err := BindSocket(path)
			require.NoError(t, err)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			go ServeSocket(ctx, ln, nil)

			conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
			require.NoError(t, err)
			defer conn.Close() //nolint:errcheck
			require.NoError(t, conn.SetDeadline(time.Now().Add(15*time.Second)))
			_, err = io.Copy(conn, io.LimitReader(zeroReader{}, MaxMessageBytes+extra))
			require.NoError(t, err)
			require.NoError(t, conn.CloseWrite())
			response, err := io.ReadAll(conn)
			require.NoError(t, err)
			assert.Equal(t, "parse_error", string(response))
		})
	}
}

func TestSocketOversizeDrainHasDeadline(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close() //nolint:errcheck
	done := make(chan struct{})
	go func() {
		handleLDAConn(server, nil)
		close(done)
	}()
	require.NoError(t, client.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := io.Copy(client, io.LimitReader(zeroReader{}, MaxMessageBytes+1))
	require.NoError(t, err)
	// Leave the write side open: the server must stop draining on its own.
	response, err := io.ReadAll(client)
	require.NoError(t, err)
	assert.Equal(t, "parse_error", string(response))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish after rejecting oversized input")
	}
}

func TestThinClientMessageSizeBoundary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "mymail-lda")
	output, err := exec.Command("go", "build", "-o", binary, "../../cmd/lda").CombinedOutput()
	require.NoError(t, err, "%s", output)

	for _, tc := range []struct {
		name string
		size int64
		code int
	}{
		{"at_limit", MaxMessageBytes, 0},
		{"over_limit", MaxMessageBytes + 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lda.sock")
			ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
			require.NoError(t, err)
			defer ln.Close() //nolint:errcheck
			require.NoError(t, ln.SetDeadline(time.Now().Add(15*time.Second)))
			type delivery struct {
				bytes int64
				err   error
			}
			done := make(chan delivery, 1)
			go func() {
				conn, err := ln.AcceptUnix()
				if err != nil {
					done <- delivery{err: err}
					return
				}
				defer conn.Close() //nolint:errcheck
				_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
				n, err := io.Copy(io.Discard, conn)
				// The over-limit client may close before reading the reply.
				_, _ = conn.Write([]byte("ok"))
				done <- delivery{bytes: n, err: err}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "-lda-socket", path)
			cmd.Stdin = io.LimitReader(zeroReader{}, tc.size)
			output, err := cmd.CombinedOutput()
			if tc.code == 0 {
				require.NoError(t, err, "%s", output)
			} else {
				var exit *exec.ExitError
				require.ErrorAs(t, err, &exit, "%s", output)
				assert.Equal(t, tc.code, exit.ExitCode(), "%s", output)
			}
			result := <-done
			require.NoError(t, result.err)
			assert.Equal(t, tc.size, result.bytes)
		})
	}
}
