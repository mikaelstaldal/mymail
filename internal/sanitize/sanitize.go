package sanitize

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var (
	// cssAllowlist is applied in both directions. Adding a property here cannot
	// reintroduce url(), expression(), or -moz-binding: bluemonday binds
	// cssValueAllowed to every property uniformly (MatchingHandler replaces the
	// per-property default), so every functional notation but the colour
	// functions stays blocked no matter which property carries it. The residual
	// risk in CSS is therefore layout, not resource loading — see the
	// deliberately-excluded list below.
	cssAllowlist = []string{
		"color", "background-color", "font-family", "font-size",
		"font-style", "font-variant", "font-weight", "letter-spacing",
		"line-height", "text-align", "text-decoration", "text-indent",
		"vertical-align", "white-space", "word-spacing",
		"border", "border-color", "border-style", "border-width",
		"border-collapse", "border-spacing",
		"padding", "margin", "width", "max-width", "height",

		// Per-side longhands of the box shorthands above. These add no
		// expressive power — `border`/`padding`/`margin` can already address
		// any single side — so permitting them is risk-neutral, and omitting
		// them would only mean silently dropping the shorter spelling.
		"margin-top", "margin-right", "margin-bottom", "margin-left",
		"padding-top", "padding-right", "padding-bottom", "padding-left",
		"border-top", "border-right", "border-bottom", "border-left",
		"border-top-width", "border-right-width", "border-bottom-width", "border-left-width",
		"border-top-style", "border-right-style", "border-bottom-style", "border-left-style",
		"border-top-color", "border-right-color", "border-bottom-color", "border-left-color",

		// Decorative and sizing. border-radius and list-style are cosmetic;
		// min-/max- are the same class as the width/height already allowed.
		"border-radius", "list-style", "list-style-type", "list-style-position",
		"min-width", "min-height", "max-height",
	}

	// Deliberately absent from cssAllowlist: position, z-index, float, display,
	// visibility, opacity and transform. Unlike the properties above, these
	// enable visual spoofing — overlaying or hiding content — which is the one
	// CSS risk the value handler cannot address. They are also poorly supported
	// by mail clients, so allowing them would cost more than it buys.

	// reAllowedCSSFunc matches a single, well-formed (non-nested) call to one
	// of the only CSS functional notations permitted in style values — the
	// color functions, e.g. "rgb(1, 2, 3)" or "rgba(0,0,0,.5)". Everything
	// else (notably url() and expression()) is rejected by cssValueAllowed.
	reAllowedCSSFunc = regexp.MustCompile(`\b(?:rgb|rgba|hsl|hsla)\([^()]*\)`)

	reNumeric = regexp.MustCompile(`^[0-9]+$`)
	reAlign   = regexp.MustCompile(`^(left|right|center|justify)$`)
	reHref    = regexp.MustCompile(`(?i)^(https?://|mailto:)`)
	// reSrc allows http/https URLs and base64-encoded non-SVG image data URIs.
	// SVG is excluded by enumerating allowed subtypes (RE2 lacks lookaheads).
	// Padding = is restricted to at most two trailing characters per the base64 spec.
	reSrc         = regexp.MustCompile(`(?i)^(https?://|data:image/(gif|jpe?g|pjpeg|png|webp|bmp|tiff?|ico|avif|apng|jfif|x-icon|vnd\.microsoft\.icon);base64,[a-zA-Z0-9+/]+={0,2}$)`)
	reExternalSrc = regexp.MustCompile(`(?i)^https?://`)
)

var alignElements = []string{
	"table", "tbody", "td", "tfoot", "th", "thead", "tr",
	"p", "h1", "h2", "h3", "h4", "h5", "h6", "div",
}

// allAllowedElements is applied in both directions. The second group is inert:
// none of those elements can load a resource, carry a URL, or host script, so
// permitting them costs nothing — and all are in bluemonday's default set of
// elements allowed without attributes, so a bare tag survives rather than being
// unwrapped.
var allAllowedElements = []string{
	"a", "b", "blockquote", "br", "code", "del", "div", "em",
	"h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img",
	"li", "ol", "p", "pre", "s", "span", "strong",
	"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul",

	"abbr", "caption", "cite", "col", "colgroup", "dd", "dfn", "dl", "dt",
	"figcaption", "figure", "ins", "kbd", "mark", "q", "samp", "small",
	"sub", "sup", "tt", "u", "var",
}

// outgoingOnlyElements and outgoingOnlyCSS are the extension point for anything
// that should be permitted in mail we send but not in mail we receive.
//
// Both are empty, deliberately. Everything currently allowed is either inert or
// strictly equivalent to what an already-allowed shorthand can express, so
// withholding it from inbound would buy no safety while costing real fidelity:
// a MyMail-to-MyMail message (including sending to yourself) would arrive
// stripped of styling this same instance was willing to send. Note in particular
// that a one-sided border written as `border-left` has no inbound-compatible
// fallback — `border: none; border-top: …` degrades to an invisible rule, not to
// a plain one — so an asymmetry here is not merely cosmetic.
//
// The seam is kept so that a future outgoing-only relaxation is a one-line
// change that cannot reach the untrusted inbound path by accident.
var (
	outgoingOnlyElements []string
	outgoingOnlyCSS      []string
)

// cssValueAllowed validates a CSS declaration value against an allowlist
// rather than a substring blocklist. bluemonday decodes hex escapes (\28) and
// lower-cases the value before calling this handler, but it leaves non-hex
// escapes (u\rl(...)) and comments intact — both of which a substring
// blocklist would miss while the browser still decodes them. We therefore
// reject any value containing a backslash escape or comment outright (no
// allowlisted property needs either), and permit functional notation only for
// an explicit set of color functions, blocking url(), expression(), and the
// like regardless of how they are spelled.
func cssValueAllowed(val string) bool {
	if strings.ContainsRune(val, '\\') {
		return false
	}
	if strings.Contains(val, "/*") || strings.Contains(val, "*/") {
		return false
	}
	// Strip the well-formed allowed function calls, then reject the value if
	// any parenthesis remains — that signals a disallowed or malformed call.
	stripped := reAllowedCSSFunc.ReplaceAllString(val, "")
	return !strings.ContainsAny(stripped, "()")
}

// newPolicy builds a bluemonday policy over the given element and CSS-property
// allowlists. Everything else — the attribute rules, the URL schemes, and the
// CSS value handler — is identical for both policies below, so the two differ
// only in what they permit, never in how they validate it.
func newPolicy(elements, cssProperties []string) *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(elements...)

	p.AllowAttrs("href").Matching(reHref).OnElements("a")
	p.AllowAttrs("src").Matching(reSrc).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	p.AllowAttrs("colspan", "rowspan").Matching(reNumeric).OnElements("td", "th")
	p.AllowAttrs("align").Matching(reAlign).OnElements(alignElements...)
	p.AllowStyles(cssProperties...).MatchingHandler(cssValueAllowed).OnElements(elements...)

	// AllowURLSchemes is required because AddTargetBlankToFullyQualifiedLinks
	// enables requireParseableURLs, which rejects any scheme not in the allowlist.
	p.AllowURLSchemes("http", "https", "mailto", "data")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)

	return p
}

// NewEmailPolicy constructs a bluemonday policy appropriate for rendering
// untrusted email HTML inside a sandboxed iframe. This is the policy for
// anything arriving from outside — use it, not the outgoing one, for inbound
// mail.
//
// It is not the narrowest allowlist that could be written, because narrowness
// is only worth paying for where the excluded thing carries risk. Inert
// elements and CSS that cannot reference a resource are permitted; what stays
// out is what can execute, fetch, or spoof. The sanitizer is also not the only
// gate here — see MessagesIDBodyGet's CSP and iframe sandbox.
func NewEmailPolicy() *bluemonday.Policy {
	return newPolicy(allAllowedElements, cssAllowlist)
}

// NewOutgoingPolicy constructs the policy for mail this instance sends. It is a
// superset of NewEmailPolicy — same attribute rules, same URL schemes, same CSS
// value handler, plus outgoingOnlyElements and outgoingOnlyCSS, both of which
// are currently empty. The two policies are therefore equivalent today, which is
// what keeps a MyMail-to-MyMail message rendering exactly as it was sent
// (TestSentHTMLSurvivesBeingReceived).
//
// The separation is kept for the seam, not for a present-day difference: the
// two directions have genuinely different threat models — inbound HTML is
// attacker-controlled, outgoing is composed by this instance's own user or a
// same-origin sibling app such as MyNotes — so if something ever needs to be
// sendable without being renderable, this is where it goes.
//
// Widening the outgoing side stays safe for the Sent/Scheduled folders, whose
// bodies are displayed through the same gates as inbound mail:
// MessagesIDBodyGet serves them with `default-src 'none'` and no script-src,
// inside an iframe sandboxed without allow-scripts or allow-same-origin. Those
// gates do not care which policy produced the stored HTML.
func NewOutgoingPolicy() *bluemonday.Policy {
	return newPolicy(
		slices.Concat(allAllowedElements, outgoingOnlyElements),
		slices.Concat(cssAllowlist, outgoingOnlyCSS),
	)
}

var (
	policy         = NewEmailPolicy()
	outgoingPolicy = NewOutgoingPolicy()
)

// HTML sanitizes untrusted (inbound) email HTML using the email policy.
func HTML(h string) string {
	return policy.Sanitize(h)
}

// OutgoingHTML sanitizes HTML composed by this instance's own user, on its way
// into a message we send. See NewOutgoingPolicy for why this is a separate,
// slightly wider allowlist.
func OutgoingHTML(h string) string {
	return outgoingPolicy.Sanitize(h)
}

const (
	maxCIDImages = 64
	maxTotalSize = 10 * 1024 * 1024
	maxImageSize = 1 * 1024 * 1024
)

// ResolveCID resolves cid: src attributes to inline data URIs, then sanitizes.
// cidMap maps Content-ID (without angle brackets) to raw bytes.
// cidContentTypes maps the same keys to MIME content types.
func ResolveCID(h string, cidMap map[string][]byte, cidContentTypes map[string]string) string {
	doc, err := html.Parse(strings.NewReader(h))
	if err != nil {
		return policy.Sanitize(h)
	}

	// Count cid: images.
	count := 0
	walkNodes(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			for _, a := range n.Attr {
				if a.Key == "src" && isCIDSrc(a.Val) {
					count++
				}
			}
		}
	})

	if count > maxCIDImages {
		walkNodes(doc, func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "img" {
				n.Attr = filterAttrs(n.Attr, func(a html.Attribute) bool {
					return a.Key != "src" || !isCIDSrc(a.Val)
				})
			}
		})
		return policy.Sanitize(renderBodyFragment(doc))
	}

	totalBytes := 0
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || n.Data != "img" {
			return
		}
		for i, a := range n.Attr {
			if a.Key != "src" || !isCIDSrc(a.Val) {
				continue
			}
			cid := a.Val[4:] // strip "cid:"
			data, found := cidLookup(cidMap, cid)
			ct := cidContentTypeLookup(cidContentTypes, cid)
			if found && len(data) <= maxImageSize && totalBytes+len(data) <= maxTotalSize {
				totalBytes += len(data)
				n.Attr[i].Val = fmt.Sprintf("data:%s;base64,%s", ct, base64.StdEncoding.EncodeToString(data))
			} else {
				n.Attr = append(n.Attr[:i], n.Attr[i+1:]...)
			}
			break
		}
	})

	return policy.Sanitize(renderBodyFragment(doc))
}

// HasExternalImages reports whether the HTML contains any <img> with an http
// or https src. data: URIs are not counted.
func HasExternalImages(h string) bool {
	z := html.NewTokenizer(strings.NewReader(h))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		if string(name) != "img" || !hasAttr {
			continue
		}
		for {
			key, val, more := z.TagAttr()
			if string(key) == "src" && reExternalSrc.Match(val) {
				return true
			}
			if !more {
				break
			}
		}
	}
}

func isCIDSrc(val string) bool {
	return strings.HasPrefix(strings.ToLower(val), "cid:")
}

func walkNodes(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walkNodes(c, fn)
	}
}

func filterAttrs(attrs []html.Attribute, keep func(html.Attribute) bool) []html.Attribute {
	result := attrs[:0:0] // new slice, no shared backing array
	for _, a := range attrs {
		if keep(a) {
			result = append(result, a)
		}
	}
	return result
}

func cidLookup(cidMap map[string][]byte, cid string) ([]byte, bool) {
	if data, ok := cidMap[cid]; ok {
		return data, true
	}
	lower := strings.ToLower(cid)
	for k, v := range cidMap {
		if strings.ToLower(k) == lower {
			return v, true
		}
	}
	return nil, false
}

func cidContentTypeLookup(cidContentTypes map[string]string, cid string) string {
	if ct, ok := cidContentTypes[cid]; ok {
		return ct
	}
	lower := strings.ToLower(cid)
	for k, v := range cidContentTypes {
		if strings.ToLower(k) == lower {
			return v
		}
	}
	return "application/octet-stream"
}

// renderBodyFragment serializes the children of the <body> element that
// html.Parse always inserts when parsing a fragment.
func renderBodyFragment(doc *html.Node) string {
	body := findNode(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "body"
	})
	if body == nil {
		var b strings.Builder
		_ = html.Render(&b, doc)
		return b.String()
	}
	var b strings.Builder
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		_ = html.Render(&b, c)
	}
	return b.String()
}

func findNode(n *html.Node, match func(*html.Node) bool) *html.Node {
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if result := findNode(c, match); result != nil {
			return result
		}
	}
	return nil
}
