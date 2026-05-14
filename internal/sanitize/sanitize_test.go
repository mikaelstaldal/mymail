package sanitize

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScriptStripped(t *testing.T) {
	out := SanitizeHTML(`<p>hello</p><script>alert(1)</script>`)
	assert.NotContains(t, out, "<script")
	assert.Contains(t, out, "hello")
}

func TestDataImagePreserved(t *testing.T) {
	src := `data:image/png;base64,iVBORw0KGgo=`
	out := SanitizeHTML(`<img src="` + src + `" alt="test">`)
	assert.Contains(t, out, src)
}

func TestJavascriptSrcStripped(t *testing.T) {
	out := SanitizeHTML(`<img src="javascript:alert(1)" alt="x">`)
	assert.NotContains(t, out, "javascript:")
}

func TestColspanAllowedOnTD(t *testing.T) {
	out := SanitizeHTML(`<table><tr><td colspan="2">cell</td></tr></table>`)
	assert.Contains(t, out, `colspan="2"`)
}

func TestColspanStrippedOnP(t *testing.T) {
	out := SanitizeHTML(`<p colspan="2">text</p>`)
	assert.NotContains(t, out, "colspan")
}

func TestStyleURLStripped(t *testing.T) {
	out := SanitizeHTML(`<p style="background: url(http://evil.com/x.png)">text</p>`)
	assert.NotContains(t, out, "url(")
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
	out := SanitizeHTML(`<img src="data:image/svg+xml;base64,PHN2Zy8+" alt="x">`)
	assert.NotContains(t, out, "svg+xml")
}

func TestLinksGetTargetBlank(t *testing.T) {
	out := SanitizeHTML(`<a href="https://example.com">link</a>`)
	assert.Contains(t, out, `target="_blank"`)
	// bluemonday emits noreferrer before noopener
	assert.Contains(t, out, "noreferrer")
	assert.Contains(t, out, "noopener")
}
