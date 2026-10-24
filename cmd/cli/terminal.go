package main

import (
	"io"
	"strings"

	"github.com/mikaelstaldal/go-server-common/termsafe"
)

func copyTerminal(dst io.Writer, src io.Reader) error {
	return termsafe.Copy(dst, src)
}

// writeCLIError applies the same display policy independently of stdout.
func writeCLIError(stderr io.Writer, err error, terminal bool) error {
	message := "mymail-cli: " + err.Error() + "\n"
	if terminal {
		return copyTerminal(stderr, strings.NewReader(message))
	}
	_, writeErr := io.WriteString(stderr, message)
	return writeErr
}
