package sanitize

import (
	"strings"
	"testing"
)

func TestScriptStripped(t *testing.T) {
	out := SanitizeHTML(`<p>hello</p><script>alert(1)</script>`)
	if strings.Contains(out, "<script") {
		t.Errorf("expected <script> to be stripped, got: %s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected paragraph text to survive, got: %s", out)
	}
}

func TestDataImagePreserved(t *testing.T) {
	src := `data:image/png;base64,iVBORw0KGgo=`
	out := SanitizeHTML(`<img src="` + src + `" alt="test">`)
	if !strings.Contains(out, src) {
		t.Errorf("expected data:image src to be preserved, got: %s", out)
	}
}

func TestJavascriptSrcStripped(t *testing.T) {
	out := SanitizeHTML(`<img src="javascript:alert(1)" alt="x">`)
	if strings.Contains(out, "javascript:") {
		t.Errorf("expected javascript: src to be stripped, got: %s", out)
	}
}

func TestColspanAllowedOnTD(t *testing.T) {
	out := SanitizeHTML(`<table><tr><td colspan="2">cell</td></tr></table>`)
	if !strings.Contains(out, `colspan="2"`) {
		t.Errorf("expected colspan to be allowed on td, got: %s", out)
	}
}

func TestColspanStrippedOnP(t *testing.T) {
	out := SanitizeHTML(`<p colspan="2">text</p>`)
	if strings.Contains(out, "colspan") {
		t.Errorf("expected colspan to be stripped on p, got: %s", out)
	}
}

func TestStyleURLStripped(t *testing.T) {
	out := SanitizeHTML(`<p style="background: url(http://evil.com/x.png)">text</p>`)
	if strings.Contains(out, "url(") {
		t.Errorf("expected style with url() to be stripped, got: %s", out)
	}
}

func TestLinksGetTargetBlank(t *testing.T) {
	out := SanitizeHTML(`<a href="https://example.com">link</a>`)
	if !strings.Contains(out, `target="_blank"`) {
		t.Errorf("expected target=_blank, got: %s", out)
	}
	// bluemonday emits noreferrer before noopener
	if !strings.Contains(out, "noreferrer") || !strings.Contains(out, "noopener") {
		t.Errorf("expected rel containing noopener and noreferrer, got: %s", out)
	}
}
