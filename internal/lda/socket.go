package lda

import (
	"context"
	"database/sql"
	"io"
	"log"
	"net"
	"os"
)

// BindSocket creates a UNIX socket listener at socketPath.
// Any stale socket file from a previous (crashed) run is removed first.
func BindSocket(socketPath string) (net.Listener, error) {
	_ = os.Remove(socketPath)
	return net.Listen("unix", socketPath)
}

// ServeSocket accepts LDA delivery connections on ln until ctx is cancelled,
// then closes the listener and removes the socket file.
// Each connection receives raw RFC 5322 bytes (client signals end with half-close),
// runs the full LDA pipeline, and writes one of "ok", "parse_error", or "transient_error".
func ServeSocket(ctx context.Context, ln net.Listener, db *sql.DB) {
	socketPath := ln.Addr().String()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(socketPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed on shutdown
		}
		go handleLDAConn(conn, db)
	}
}

func handleLDAConn(conn net.Conn, db *sql.DB) {
	defer func(conn net.Conn) {
		_ = conn.Close()
	}(conn)

	// Bound the read: a single message must not be able to exhaust memory.
	// Read one byte past the limit so we can distinguish "at limit" from "over".
	raw, err := io.ReadAll(io.LimitReader(conn, MaxMessageBytes+1))
	if err != nil {
		log.Printf("lda socket: read message: %v", err)
		_, _ = conn.Write([]byte("transient_error"))
		return
	}
	if int64(len(raw)) > MaxMessageBytes {
		log.Printf("lda socket: message exceeds %d bytes, rejecting", MaxMessageBytes)
		_, _ = conn.Write([]byte("parse_error"))
		return
	}

	code := runCore(db, raw)

	var resp string
	switch code {
	case 0:
		resp = "ok"
	case 1:
		resp = "parse_error"
	default:
		resp = "transient_error"
	}

	if _, err := conn.Write([]byte(resp)); err != nil {
		log.Printf("lda socket: write response: %v", err)
	}
}
