package sanitize

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
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
	//
	// The one exception is `display: none`, admitted by newPolicy with that
	// value only. Senders use it to hide a preheader (the inbox preview text),
	// typically alongside `max-height: 0; max-width: 0; overflow: hidden`.
	// Dropping only the display declaration leaves a zero-sized box whose text
	// overflows it, piling every word on top of the next. Inbound normalization
	// removes each element it hides (removeHidden) rather than storing it. Not
	// quite always: on markup bounded by BoundFormatting, a hidden formatting
	// element beyond the bound is dropped as a tag and its content is shown,
	// and the parser-depth fallback stores the declaration as it is.

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
	// bluemonday passes an empty value when decoding a non-BMP, surrogate,
	// or out-of-range hex escape fails, but emits the original declaration
	// if we accept it. Fail closed rather than approve an unvalidated value.
	if strings.TrimSpace(val) == "" {
		return false
	}
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
	// A browser never renders template content, so neither do we. bluemonday
	// would otherwise unwrap the element and show it.
	p.SkipElementsContent("template")

	p.AllowAttrs("href").Matching(reHref).OnElements("a")
	p.AllowAttrs("src").Matching(reSrc).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	p.AllowAttrs("colspan", "rowspan").Matching(reNumeric).OnElements("td", "th")
	p.AllowAttrs("align").Matching(reAlign).OnElements(alignElements...)
	p.AllowStyles(cssProperties...).MatchingHandler(cssValueAllowed).OnElements(elements...)
	p.AllowStyles("display").MatchingEnum("none").OnElements(elements...)

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
// gate here — see MessagesIDBodyGet's CSP sandbox and the iframe sandbox.
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
// with a CSP sandbox without allow-scripts or allow-same-origin, also enforced
// by the iframe attribute. Those gates do not care which policy produced the stored HTML.
func NewOutgoingPolicy() *bluemonday.Policy {
	return newPolicy(
		slices.Concat(allAllowedElements, outgoingOnlyElements),
		slices.Concat(cssAllowlist, outgoingOnlyCSS),
	)
}

var (
	policy         = NewEmailPolicy()
	outgoingPolicy = NewOutgoingPolicy()
	// softWrapPolicy is the outgoing policy plus class and comments, which
	// joinSoftWrappedParagraphs reads. The outgoing pass strips both after.
	softWrapPolicy = newSoftWrapPolicy()
)

func newSoftWrapPolicy() *bluemonday.Policy {
	elements := slices.Concat(allAllowedElements, outgoingOnlyElements)
	p := newPolicy(elements, slices.Concat(cssAllowlist, outgoingOnlyCSS))
	p.AllowAttrs("class").OnElements(elements...)
	p.AllowComments()
	return p
}

// HTML sanitizes untrusted (inbound) email HTML using the email policy.
func HTML(h string) string {
	return policy.Sanitize(h)
}

// OutgoingHTML joins editor-created visual wraps, then sanitizes HTML composed
// by this instance's own user on its way into a message we send. See
// NewOutgoingPolicy for the separate outgoing allowlist.
func OutgoingHTML(h string) string {
	return outgoingPolicy.Sanitize(joinSoftWrappedParagraphs(h))
}

// Quill represents a visual wrap as a paragraph with this class. Join only
// those marked boundaries before the sanitizer removes the class, preserving
// actual Enter-created paragraphs and the draft's editable representation.
const softWrapClass = "ql-softwrap-y"

func joinSoftWrappedParagraphs(h string) string {
	if !strings.Contains(h, softWrapClass) {
		return h
	}
	// Sanitized and bounded first, like inbound HTML, so the parse cannot
	// amplify (see BoundFormatting).
	h = BoundFormatting(softWrapPolicy.Sanitize(h))
	root := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(h), root)
	if err != nil {
		return h
	}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var join func(*html.Node)
	join = func(parent *html.Node) {
		for n := parent.FirstChild; n != nil; n = n.NextSibling {
			if n.Type == html.ElementNode && n.Data == "p" {
				continued := hasSoftWrap(n)
				for continued {
					next := n.NextSibling
					for next != nil && next.Type == html.TextNode && strings.TrimSpace(next.Data) == "" {
						next = next.NextSibling
					}
					if next == nil || next.Type != html.ElementNode || next.Data != "p" || hasOwnBlockFormat(next) {
						break
					}
					for gap := n.NextSibling; gap != next; {
						following := gap.NextSibling
						parent.RemoveChild(gap)
						gap = following
					}
					n.AppendChild(&html.Node{Type: html.TextNode, Data: " "})
					for next.FirstChild != nil {
						child := next.FirstChild
						next.RemoveChild(child)
						n.AppendChild(child)
					}
					// A run continues only when the next line also ends in a soft break.
					continued = hasSoftWrap(next)
					parent.RemoveChild(next)
				}
			}
			join(n)
		}
	}
	join(root)
	var out strings.Builder
	for n := root.FirstChild; n != nil; n = n.NextSibling {
		if err := html.Render(&out, n); err != nil {
			return h
		}
	}
	return out.String()
}

func hasSoftWrap(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, name := range strings.Fields(a.Val) {
				if name == softWrapClass {
					return true
				}
			}
		}
	}
	return false
}

func hasOwnBlockFormat(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "class" {
			for _, name := range strings.Fields(a.Val) {
				if strings.HasPrefix(name, "ql-align-") || strings.HasPrefix(name, "ql-indent-") {
					return true
				}
			}
		}
	}
	return false
}

const (
	maxCIDImages = 64
	maxCIDLength = 1024
	maxTotalSize = 10 * 1024 * 1024
	maxImageSize = 1 * 1024 * 1024
)

// Limits on what normalizing inbound HTML may produce. Output beyond
// maxAmplification times its input (plus amplificationSlack) is treated as an
// amplification attack and the message is normalized again with every
// formatting element removed, which keeps the output linear in the input.
const (
	maxAmplification   = 8
	amplificationSlack = 64 << 10
)

// ResolveCID resolves cid: src attributes to inline data URIs, then sanitizes.
// It also returns the Content-IDs actually embedded, for excluding those MIME
// parts from the attachment list. CID values longer than maxCIDLength are
// rejected before lookup or copying.
// cidMap maps Content-ID (without angle brackets) to raw bytes.
// cidContentTypes maps the same keys to MIME content types.
//
// The attacker's markup never reaches html.Parse: the cid: rewrite and the
// sanitizer are tokenizer-based, and only sanitized markup with a bounded
// number of active formatting elements is parsed for normalization.
func ResolveCID(h string, cidMap map[string][]byte, cidContentTypes map[string]string) (string, map[string]bool) {
	used := make(map[string]bool)
	rewritten, candidates := rewriteCIDs(h, cidMap, cidContentTypes)
	clean := normalize(policy.Sanitize(rewritten), len(rewritten))
	z := html.NewTokenizer(strings.NewReader(clean))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
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
			if string(key) == "src" {
				for _, cid := range candidates[string(val)] {
					used[cid] = true
				}
			}
			if !more {
				break
			}
		}
	}
	return clean, used
}

// rewriteCIDs replaces each cid: img src with a data URI, or removes it when
// the image is missing or over a limit. Beyond maxCIDImages it removes them
// all. A resolved image can still be removed with its parent by the HTML
// sanitizer (for example, inside <object>), so it returns candidates by URI,
// to count as used only if an img src survives sanitization.
func rewriteCIDs(h string, cidMap map[string][]byte, cidContentTypes map[string]string) (string, map[string][]string) {
	candidates := make(map[string][]string)
	count := 0
	z := html.NewTokenizer(strings.NewReader(h))
	for tt := z.Next(); tt != html.ErrorToken; tt = z.Next() {
		if tt == html.StartTagToken || tt == html.SelfClosingTagToken {
			if tok := z.Token(); tok.Data == "img" {
				if i := attrIndex(tok.Attr, "src"); i >= 0 && isCIDSrc(tok.Attr[i].Val) {
					count++
				}
			}
		}
	}
	if count == 0 {
		return h, candidates
	}

	var out strings.Builder
	totalBytes := 0
	z = html.NewTokenizer(strings.NewReader(h))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			out.Write(z.Raw())
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			out.Write(z.Raw())
			continue
		}
		raw := z.Raw()
		tok := z.Token()
		i := attrIndex(tok.Attr, "src")
		if tok.Data != "img" || i < 0 || !isCIDSrc(tok.Attr[i].Val) {
			out.Write(raw)
			continue
		}
		cid := tok.Attr[i].Val[4:] // strip "cid:"
		uri := ""
		if count <= maxCIDImages && len(cid) > 0 && len(cid) <= maxCIDLength {
			data, found := cidLookup(cidMap, cid)
			if found && len(data) <= maxImageSize && totalBytes+len(data) <= maxTotalSize {
				ct := cidContentTypeLookup(cidContentTypes, cid)
				if u := fmt.Sprintf("data:%s;base64,%s", ct, base64.StdEncoding.EncodeToString(data)); reSrc.MatchString(u) {
					totalBytes += len(data)
					uri = u
					candidates[uri] = append(candidates[uri], strings.ToLower(cid))
				}
			}
		}
		// Drop every src, keeping the resolved one: the tree builder and the
		// sanitizer would both honour only the first.
		tok.Attr = filterAttrs(tok.Attr, func(a html.Attribute) bool { return a.Key != "src" })
		if uri != "" {
			tok.Attr = slices.Insert(tok.Attr, min(i, len(tok.Attr)), html.Attribute{Key: "src", Val: uri})
		}
		out.WriteString(tok.String())
	}
	return out.String(), candidates
}

func attrIndex(attrs []html.Attribute, key string) int {
	for i, a := range attrs {
		if a.Key == key {
			return i
		}
	}
	return -1
}

// normalize parses sanitized markup and serializes it back, then sanitizes
// the result again. The parse sees at most MaxActiveFormatting active
// formatting elements; if the output still exceeds the amplification limit
// (cloned elements can carry long style attributes) every formatting element
// is removed instead. inputLen is the length the limit is relative to.
func normalize(clean string, inputLen int) string {
	limit := maxAmplification*inputLen + amplificationSlack
	if out, ok := renderNormalized(boundFormatting(clean, MaxActiveFormatting), limit); ok {
		return out
	}
	stripped := boundFormatting(clean, 0)
	if out, ok := renderNormalized(stripped, -1); ok {
		return out
	}
	// Only nesting beyond the parser's depth limit gets here.
	return stripped
}

func renderNormalized(h string, limit int) (string, bool) {
	doc, err := html.Parse(strings.NewReader(h))
	if err != nil {
		return "", false
	}
	removeHidden(doc)
	w := &limitedBuilder{limit: limit}
	if !renderBodyFragment(w, doc) {
		return "", false
	}
	return policy.Sanitize(w.String()), true
}

// removeHidden removes every element styled `display: none`, with its
// content. Any display declaration that survived the policy means none: it
// admits no other value. Only the property name is checked, because bluemonday
// matches the decoded value but keeps the sender's spelling, such as `\6e one`.
func removeHidden(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.ElementNode && isHidden(c) {
			n.RemoveChild(c)
		} else {
			removeHidden(c)
		}
		c = next
	}
}

func isHidden(n *html.Node) bool {
	i := attrIndex(n.Attr, "style")
	if i < 0 {
		return false
	}
	for _, decl := range strings.Split(n.Attr[i].Val, ";") {
		prop, _, ok := strings.Cut(decl, ":")
		if ok && strings.EqualFold(strings.TrimSpace(prop), "display") {
			return true
		}
	}
	return false
}

var errOutputLimit = errors.New("output limit exceeded")

// limitedBuilder fails writes beyond limit bytes; a negative limit is none.
type limitedBuilder struct {
	strings.Builder
	limit int
}

func (b *limitedBuilder) Write(p []byte) (int, error) {
	if b.limit >= 0 && b.Len()+len(p) > b.limit {
		return 0, errOutputLimit
	}
	return b.Builder.Write(p)
}

func (b *limitedBuilder) WriteByte(c byte) error {
	if b.limit >= 0 && b.Len()+1 > b.limit {
		return errOutputLimit
	}
	return b.Builder.WriteByte(c)
}

func (b *limitedBuilder) WriteString(s string) (int, error) {
	if b.limit >= 0 && b.Len()+len(s) > b.limit {
		return 0, errOutputLimit
	}
	return b.Builder.WriteString(s)
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
	return len(val) >= 4 && strings.EqualFold(val[:4], "cid:")
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
// html.Parse always inserts when parsing a fragment. It reports false if
// writing fails.
func renderBodyFragment(w io.Writer, doc *html.Node) bool {
	body := findNode(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "body"
	})
	if body == nil {
		return html.Render(w, doc) == nil
	}
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		if html.Render(w, c) != nil {
			return false
		}
	}
	return true
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
