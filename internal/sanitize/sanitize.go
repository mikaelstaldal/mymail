package sanitize

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var (
	cssAllowlist = []string{
		"color", "background-color", "font-family", "font-size",
		"font-style", "font-variant", "font-weight", "letter-spacing",
		"line-height", "text-align", "text-decoration", "text-indent",
		"vertical-align", "white-space", "word-spacing",
		"border", "border-color", "border-style", "border-width",
		"border-collapse", "border-spacing",
		"padding", "margin", "width", "max-width", "height",
	}

	cssForbiddenSubstrings = []string{"url(", "expression(", "-moz-binding", "/*"}

	reNumeric     = regexp.MustCompile(`^[0-9]+$`)
	reAlign       = regexp.MustCompile(`^(left|right|center|justify)$`)
	reHref        = regexp.MustCompile(`(?i)^(https?://|mailto:)`)
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

var allAllowedElements = []string{
	"a", "b", "blockquote", "br", "code", "del", "div", "em",
	"h1", "h2", "h3", "h4", "h5", "h6", "hr", "i", "img",
	"li", "ol", "p", "pre", "s", "span", "strong",
	"table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul",
}

func cssValueAllowed(val string) bool {
	for _, forbidden := range cssForbiddenSubstrings {
		if strings.Contains(val, forbidden) {
			return false
		}
	}
	return true
}

// NewEmailPolicy constructs a bluemonday policy appropriate for rendering
// untrusted email HTML inside a sandboxed iframe.
func NewEmailPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(allAllowedElements...)

	p.AllowAttrs("href").Matching(reHref).OnElements("a")
	p.AllowAttrs("src").Matching(reSrc).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	p.AllowAttrs("colspan", "rowspan").Matching(reNumeric).OnElements("td", "th")
	p.AllowAttrs("align").Matching(reAlign).OnElements(alignElements...)
	p.AllowStyles(cssAllowlist...).MatchingHandler(cssValueAllowed).OnElements(allAllowedElements...)

	// AllowURLSchemes is required because AddTargetBlankToFullyQualifiedLinks
	// enables requireParseableURLs, which rejects any scheme not in the allowlist.
	p.AllowURLSchemes("http", "https", "mailto", "data")
	p.AddTargetBlankToFullyQualifiedLinks(true)
	p.RequireNoReferrerOnFullyQualifiedLinks(true)

	return p
}

var policy = NewEmailPolicy()

// SanitizeHTML sanitizes html using the email policy.
func SanitizeHTML(h string) string {
	return policy.Sanitize(h)
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
					return !(a.Key == "src" && isCIDSrc(a.Val))
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
