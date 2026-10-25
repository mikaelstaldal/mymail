package phishing

import (
	"strings"

	"github.com/mikaelstaldal/mymail/internal/sanitize"
	"golang.org/x/net/html"
)

// parseBody matches the standards-mode document served by MessagesIDBodyGet.
// Both analysis and rewriting must use this tree so anchor indices agree.
// Bound formatting first, including for bodies stored before ingestion did so.
func parseBody(body string) (*html.Node, error) {
	return html.Parse(strings.NewReader("<!DOCTYPE html>" + sanitize.BoundFormatting(body)))
}
