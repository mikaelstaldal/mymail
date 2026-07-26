package sanitize

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScriptStripped(t *testing.T) {
	out := HTML(`<p>hello</p><script>alert(1)</script>`)
	assert.NotContains(t, out, "<script")
	assert.Contains(t, out, "hello")
}

func TestDataImagePreserved(t *testing.T) {
	src := `data:image/png;base64,iVBORw0KGgo=`
	out := HTML(`<img src="` + src + `" alt="test">`)
	assert.Contains(t, out, src)
}

func TestJavascriptSrcStripped(t *testing.T) {
	out := HTML(`<img src="javascript:alert(1)" alt="x">`)
	assert.NotContains(t, out, "javascript:")
}

func TestColspanAllowedOnTD(t *testing.T) {
	out := HTML(`<table><tr><td colspan="2">cell</td></tr></table>`)
	assert.Contains(t, out, `colspan="2"`)
}

func TestColspanStrippedOnP(t *testing.T) {
	out := HTML(`<p colspan="2">text</p>`)
	assert.NotContains(t, out, "colspan")
}

func TestStyleURLStripped(t *testing.T) {
	out := HTML(`<p style="background: url(http://evil.com/x.png)">text</p>`)
	assert.NotContains(t, out, "url(")
}

// TestCSSValueAllowed exercises the value allowlist directly. bluemonday lower-
// cases and decodes hex escapes (\28) before invoking this handler, so the
// inputs here are written as the handler would receive them.
func TestCSSValueAllowed(t *testing.T) {
	allowed := []string{
		"red",
		"#ff0000",
		"rgb(1, 2, 3)",
		"rgba(0,0,0,.5)",
		"hsl(120, 50%, 50%)",
		"hsla(120,50%,50%,.5)",
		"arial, sans-serif",
		"12px",
		"1em",
		"100%",
	}
	forbidden := []string{
		// Disallowed functions, however spelled.
		"url(http://evil.com/x)",
		"expression(alert(1))",
		"image-set(http://evil.com/x)",
		// Non-hex backslash escape that the browser decodes back to url().
		`u\rl(http://evil.com/x)`,
		// CSS comments used to split tokens.
		"red /* x */",
		"u/**/rl(http://evil.com/x)",
		// Stray / unbalanced parentheses.
		"(",
		")",
		"foo)",
	}
	for _, v := range allowed {
		assert.True(t, cssValueAllowed(v), "expected allowed: %q", v)
	}
	for _, v := range forbidden {
		assert.False(t, cssValueAllowed(v), "expected forbidden: %q", v)
	}
}

// TestStyleEscapeBypassStripped verifies that CSS escape sequences cannot be
// used to smuggle a forbidden function (e.g. url()) past the value allowlist.
func TestStyleEscapeBypassStripped(t *testing.T) {
	cases := []string{
		// hex escape decoded to "url(" by bluemonday before the handler runs
		`<p style="color: u\72l(http://evil.com/x)">text</p>`,
		`<p style="color: url\28 http://evil.com/x\29">text</p>`,
		// non-hex backslash escape decoded to "url(" by the browser
		`<p style="color: u\rl(http://evil.com/x)">text</p>`,
		// comment-split function name
		`<p style="color: u/**/rl(http://evil.com/x)">text</p>`,
	}
	for _, c := range cases {
		out := HTML(c)
		assert.NotContains(t, out, "evil.com", "input: %s", c)
		assert.NotContains(t, out, "url", "input: %s", c)
	}
}

// TestStyleColorFunctionsPreserved confirms the value allowlist keeps the
// legitimate color functions that real email styling relies on.
func TestStyleColorFunctionsPreserved(t *testing.T) {
	out := HTML(`<p style="color: rgb(255, 0, 0); background-color: rgba(0,0,0,.5)">text</p>`)
	assert.Contains(t, out, "rgb(255, 0, 0)")
	assert.Contains(t, out, "rgba(0,0,0,.5)")
}

func TestReSrc(t *testing.T) {
	allowed := []string{
		"https://example.com/image.png",
		"http://example.com/image.gif",
		"data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAAAAAAALAAAAAABAAEAAAICTAEAOw==",
		"data:image/jpeg;base64,/9j/4AAQSkZJRgAB",
		"data:image/jpg;base64,/9j/4AAQSkZJRgAB",
		"data:image/pjpeg;base64,/9j/4AAQSkZJRgAB",
		"data:image/png;base64,iVBORw0KGgo=",
		"data:image/webp;base64,UklGRiQA",
		"data:image/bmp;base64,Qk0=",
		"data:image/tiff;base64,SUkq",
		"data:image/tif;base64,SUkq",
		"data:image/ico;base64,AAABAA==",
		"data:image/avif;base64,AAAAFGZ0eXBh",
		"data:image/apng;base64,iVBORw0KGgo=",
		"data:image/jfif;base64,/9j/4AAQ",
		"data:image/x-icon;base64,AAABAA==",
		"data:image/vnd.microsoft.icon;base64,AAABAA==",
		// case-insensitive
		"data:image/PNG;base64,iVBORw0KGgo=",
		"data:image/JPEG;base64,/9j/4AAQ",
		"DATA:IMAGE/GIF;BASE64,R0lGODlh",
	}
	rejected := []string{
		// SVG must be blocked (can contain scripts)
		"data:image/svg+xml;base64,PHN2Zy8+",
		"data:image/svg;base64,PHN2Zy8+",
		"DATA:IMAGE/SVG+XML;BASE64,PHN2Zy8+",
		// other unsafe schemes
		"javascript:alert(1)",
		"data:text/html;base64,PHNjcmlwdD4=",
		"data:application/pdf;base64,JVBERi0=",
		// empty base64 payload
		"data:image/png;base64,",
		// base64 with = in the middle (invalid padding position)
		"data:image/png;base64,iVBOR=w0KGgo=",
		// more than two padding chars
		"data:image/png;base64,iVBORw0KGgo===",
		// unknown image subtype
		"data:image/unknown-type;base64,iVBORw0KGgo=",
	}

	for _, s := range allowed {
		assert.Regexp(t, reSrc, s)
	}
	for _, s := range rejected {
		assert.NotRegexp(t, reSrc, s)
	}
}

func TestSVGDataURIStripped(t *testing.T) {
	out := HTML(`<img src="data:image/svg+xml;base64,PHN2Zy8+" alt="x">`)
	assert.NotContains(t, out, "svg+xml")
}

func TestLinksGetTargetBlank(t *testing.T) {
	out := HTML(`<a href="https://example.com">link</a>`)
	assert.Contains(t, out, `target="_blank"`)
	// bluemonday emits noreferrer before noopener
	assert.Contains(t, out, "noreferrer")
	assert.Contains(t, out, "noopener")
}

// ---------------------------------------------------------------------------
// Outgoing policy — the wider allowlist applied to mail this instance sends.
// ---------------------------------------------------------------------------

// Inert elements are allowed in BOTH directions. They carry no URL, load no
// resource and host no script, so withholding them from inbound would only mean
// a MyMail-to-MyMail message arrives stripped of markup this instance sends.
func TestInertElementsAllowedBothDirections(t *testing.T) {
	// text is the character data the element wraps; it must survive either way.
	for _, tc := range []struct{ name, in, want, text string }{
		{"sup", `<p>x<sup>2</sup></p>`, "<sup>", "2"},
		{"sub", `<p>H<sub>2</sub>O</p>`, "<sub>", "2"},
		{"mark", `<p><mark>hi</mark></p>`, "<mark>", "hi"},
		{"small", `<p><small>fine print</small></p>`, "<small>", "fine print"},
		{"definition list", `<dl><dt>term</dt><dd>meaning</dd></dl>`, "<dl>", "meaning"},
		{"caption", `<table><caption>Cap</caption></table>`, "<caption>", "Cap"},
		{"colgroup", `<table><colgroup><col></colgroup></table>`, "<colgroup>", ""},
		{"figure", `<figure><figcaption>c</figcaption></figure>`, "<figure>", "c"},
		{"abbr", `<p><abbr>HTML</abbr></p>`, "<abbr>", "HTML"},
		{"kbd", `<p><kbd>Ctrl</kbd></p>`, "<kbd>", "Ctrl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Contains(t, OutgoingHTML(tc.in), tc.want, "outgoing must keep it")
			assert.Contains(t, HTML(tc.in), tc.want, "inbound must keep it too")
			if tc.text != "" {
				assert.Contains(t, HTML(tc.in), tc.text, "content must survive")
			}
		})
	}
}

// bluemonday re-serializes declarations with a space after the colon
// ("padding-left: 1.5em"), so compare against a space-free normalization rather
// than the literal input.
func normalizeDecls(s string) string {
	return strings.ReplaceAll(s, ": ", ":")
}

// The per-side longhands and decorative properties are likewise allowed in both
// directions. The longhands add no expressive power over the box shorthands
// already permitted, and — importantly — have no inbound-compatible fallback: a
// one-sided border rewritten as `border:none;border-top:…` degrades to an
// invisible rule, not a plain one.
func TestPerSideAndDecorativeCSSAllowedBothDirections(t *testing.T) {
	for _, tc := range []struct{ name, decl string }{
		{"border-left", "border-left:4px solid #d97706"},
		{"border-radius", "border-radius:6px"},
		{"padding-left", "padding-left:1.5em"},
		{"margin-top", "margin-top:0.75em"},
		{"list-style", "list-style:none"},
		{"border-top-color", "border-top-color:#e5e7eb"},
		{"min-width", "min-width:10px"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := `<p style="` + tc.decl + `">x</p>`
			assert.Contains(t, normalizeDecls(OutgoingHTML(in)), tc.decl, "outgoing must keep it")
			assert.Contains(t, normalizeDecls(HTML(in)), tc.decl, "inbound must keep it too")
		})
	}
}

// The spoofing-capable properties stay out of both policies.
func TestOutgoingStillRejectsLayoutCSS(t *testing.T) {
	for _, decl := range []string{
		"position:fixed", "z-index:9999", "display:none",
		"visibility:hidden", "opacity:0", "float:left", "transform:scale(2)",
	} {
		in := `<p style="` + decl + `">x</p>`
		assert.NotContains(t, normalizeDecls(OutgoingHTML(in)), decl, "outgoing must reject %q", decl)
		assert.NotContains(t, normalizeDecls(HTML(in)), decl, "inbound must reject %q", decl)
	}
}

// The security gates are identical in both directions — the outgoing policy is
// wider only in which inert elements and CSS properties it permits. This is the
// regression test that matters: it must keep passing as the allowlists grow.
func TestOutgoingKeepsEverySecurityGate(t *testing.T) {
	for _, tc := range []struct{ name, in, mustNotContain string }{
		{"script element", `<p>ok</p><script>alert(1)</script>`, "<script"},
		{"style element", `<style>body{color:red}</style>`, "<style"},
		{"style element content", `<style>body{color:red}</style>`, "color:red"},
		{"svg", `<svg onload="alert(1)"><path/></svg>`, "<svg"},
		{"event handler", `<p onclick="alert(1)">x</p>`, "onclick"},
		{"javascript: href", `<a href="javascript:alert(1)">x</a>`, "javascript:"},
		{"javascript: src", `<img src="javascript:alert(1)" alt="x">`, "javascript:"},
		{"css url()", `<p style="background: url(http://evil.com/x.png)">x</p>`, "url("},
		{"css url() on new property", `<p style="list-style-image: url(http://evil.com/x.png)">x</p>`, "url("},
		{"css escape bypass", `<p style="background: u\72 l(http://evil.com/x.png)">x</p>`, "l("},
		{"css comment bypass", `<p style="border-left: 1px/*x*/solid red">x</p>`, "/*"},
		{"expression()", `<p style="border-radius: expression(alert(1))">x</p>`, "expression("},
		{"svg data URI", `<img src="data:image/svg+xml;base64,PHN2Zy8+" alt="x">`, "svg+xml"},
		{"iframe", `<iframe src="https://evil.com"></iframe>`, "<iframe"},
		{"form", `<form action="https://evil.com"><input name="p"></form>`, "<form"},
		{"object", `<object data="https://evil.com/x"></object>`, "<object"},
		{"class attribute", `<p class="x">text</p>`, "class="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotContains(t, OutgoingHTML(tc.in), tc.mustNotContain)
			assert.NotContains(t, HTML(tc.in), tc.mustNotContain)
		})
	}
}

// The outgoing policy must be a superset: anything the inbound policy keeps, it
// keeps too. (Today the two are equivalent — outgoingOnly* are empty — but this
// must hold however they diverge.)
func TestOutgoingIsSupersetOfInbound(t *testing.T) {
	for _, in := range []string{
		`<p style="color:#1f2937;margin:0.75em 0">text</p>`,
		`<a href="https://example.com">link</a>`,
		`<img src="data:image/png;base64,iVBORw0KGgo=" alt="x">`,
		`<table><tr><td colspan="2" style="border:1px solid #e5e7eb">c</td></tr></table>`,
		`<blockquote><h1>h</h1><ul><li>i</li></ul></blockquote>`,
	} {
		assert.Equal(t, HTML(in), OutgoingHTML(in), "superset must not alter what inbound keeps")
	}
}

// Declaration order must survive sanitization: a shorthand followed by a
// longhand that refines it (border, then border-left) only works if the two are
// re-emitted in the order written. MyNotes' email export relies on this for the
// accent edge of a callout.
func TestDeclarationOrderPreserved(t *testing.T) {
	out := OutgoingHTML(`<p style="border:1px solid #e5e7eb;border-left:4px solid #d97706">x</p>`)
	shorthand := strings.Index(out, "border:")
	longhand := strings.Index(out, "border-left:")
	assert.NotEqual(t, -1, shorthand, "shorthand dropped: %s", out)
	assert.NotEqual(t, -1, longhand, "longhand dropped: %s", out)
	assert.Less(t, shorthand, longhand, "shorthand must precede longhand: %s", out)
}

// MyMail must be able to render everything MyMail is willing to send. A message
// sent to another MyMail instance (or to yourself) is sanitized on the way out
// by the outgoing policy and again on delivery by the inbound one; if the second
// pass changes anything, the recipient sees a degraded version of what was sent.
//
// This is the regression gate for that round trip. It fails the moment
// outgoingOnly* gains an entry, which is intentional: adding one is a decision
// to accept exactly this degradation, and it should not be possible to make it
// by accident.
func TestSentHTMLSurvivesBeingReceived(t *testing.T) {
	for _, name := range []string{
		"callout", "task list", "plain quote", "rule", "tag pill", "code", "rich text",
	} {
		in := roundTripSamples[name]
		sent := OutgoingHTML(in)
		received := HTML(sent)
		assert.Equal(t, sent, received, "%s: what we send must survive being received", name)
	}
}

// Fragments mirroring what MyNotes' email export emits (web/ts/util/emailhtml.ts).
var roundTripSamples = map[string]string{
	"callout": `<blockquote style="margin:0.75em 0;padding:0.6em 1em;border:1px solid #e5e7eb;` +
		`border-left:4px solid #d97706;border-radius:6px;background-color:#fcf4eb;color:#1f2937">` +
		`<p style="margin:0;font-weight:600;color:#d97706">Careful</p></blockquote>`,
	"task list": `<ul style="margin:0.75em 0;padding-left:1.5em">` +
		`<li style="margin:0.25em 0;list-style:none"><span>&#9745; </span> done</li></ul>`,
	"plain quote": `<blockquote style="margin:0.75em 0;padding:0.5em 1em;border-left:3px solid #e5e7eb;color:#6b7280">q</blockquote>`,
	"rule":        `<hr style="margin:1.5em 0;border:none;border-top:1px solid #e5e7eb">`,
	"tag pill": `<a href="https://example.com/tags/demo" style="color:#2563eb;text-decoration:none;` +
		`background-color:#f9fafb;border:1px solid #e5e7eb;border-radius:999px;padding:0 0.5em">tag</a>`,
	"code": `<pre style="border:1px solid #e5e7eb;border-radius:6px;white-space:pre-wrap"><code>x</code></pre>`,
	"rich text": `<p>H<sub>2</sub>O and x<sup>2</sup>, <mark>marked</mark>, <abbr>HTML</abbr>, ` +
		`<kbd>Ctrl</kbd>, <small>fine</small></p><dl><dt>term</dt><dd>meaning</dd></dl>`,
}
