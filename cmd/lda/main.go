// Command lda is a minimal Local Delivery Agent that forwards incoming RFC 5322
// messages to a running mymail server via a UNIX socket.
//
// Memory footprint is kept small by having no SQLite dependency; the server
// process handles all database access.  If the socket is unreachable the
// process exits 75 so the MTA retries delivery later.
//
// Usage:
//
//	mymail-lda -lda-socket /run/mymail/lda.sock
//
// Postfix example:
//
//	mailbox_command = /usr/local/bin/mymail-lda -lda-socket /run/mymail/lda.sock
package main

import (
	"flag"
	"io"
	"log"
	"net"
	"os"
	"time"
)

func main() {
	socketPath := flag.String("lda-socket", "", "Path to mymail LDA UNIX socket (required)")
	flag.Parse()

	if *socketPath == "" {
		log.Fatal("lda: -lda-socket is required")
	}

	conn, err := net.DialTimeout("unix", *socketPath, 30*time.Second)
	if err != nil {
		log.Printf("lda: connect to %s: %v", *socketPath, err)
		os.Exit(75)
	}
	defer conn.Close() //nolint:errcheck

	if _, err := io.Copy(conn, os.Stdin); err != nil {
		log.Printf("lda: send message: %v", err)
		os.Exit(75)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		log.Printf("lda: close write: %v", err)
		os.Exit(75)
	}

	resp, err := io.ReadAll(conn)
	if err != nil {
		log.Printf("lda: read response: %v", err)
		os.Exit(75)
	}

	switch string(resp) {
	case "ok":
		os.Exit(0)
	case "parse_error":
		os.Exit(1)
	case "transient_error":
		os.Exit(75)
	default:
		log.Printf("lda: unexpected response %q", string(resp))
		os.Exit(75)
	}
}
