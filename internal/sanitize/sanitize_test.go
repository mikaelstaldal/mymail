package sanitize

import (
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
