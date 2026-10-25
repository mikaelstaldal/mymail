package phishing

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteDeepDocument(t *testing.T) {
	body := strings.Repeat("<div>", 600) + `<a>skip</a><a href="https://evil.org" target="_blank" rel="noopener noreferrer">example.com</a>` + strings.Repeat("</div>", 600)
	m := model.DBMessage{FolderID: 1, BodyHTML: body}
	warnings := ComputeWithLinks(m, nil, Context{}, Links(body))
	require.NotEmpty(t, warnings)
	result := RewriteLinks(body, 42, warnings)
	assert.Contains(t, result, `href="../../../../#/link/42/1"`)
	assert.Contains(t, result, `target="_blank" rel="noopener noreferrer"`)
	assert.NotContains(t, result, `href="https://evil.org"`)
	assert.Equal(t, body, RewriteLinks(body, 42, []api.MessageWarning{}))
}

func TestLinksBeyondCapDisabled(t *testing.T) {
	for _, depth := range []int{0, 600} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			body := strings.Repeat("<div>", depth) + strings.Repeat(`<a></a><a href="https://a.example/"></a>`, MaxLinks/2) + `<a href="https://evil.example/login">paypal.com</a>` + strings.Repeat("</div>", depth)
			links := Links(body)
			require.Len(t, links, MaxLinks+1)
			require.Equal(t, MaxLinks, links[MaxLinks].Index)
			warnings := ComputeWithLinks(model.DBMessage{FolderID: 1, BodyHTML: body}, nil, Context{}, links)
			result := RewriteLinks(body, 42, warnings)
			assert.NotContains(t, result, fmt.Sprintf(`href="../../../../#/link/42/%d"`, MaxLinks))
			assert.Contains(t, warnings, api.MessageWarning{Kind: api.MessageWarningKindTooManyLinks})
			assert.NotContains(t, result, `href="https://evil.example/login"`)
		})
	}
}

func TestExactLinkLimitHasNoOverflow(t *testing.T) {
	for _, depth := range []int{0, 600} {
		body := strings.Repeat("<div>", depth) + strings.Repeat(`<a href="https://a.example/">plain</a>`, MaxLinks) + strings.Repeat("</div>", depth)
		links := Links(body)
		require.Len(t, links, MaxLinks)
		warnings := ComputeWithLinks(model.DBMessage{FolderID: 1, BodyHTML: body}, nil, Context{}, links)
		assert.NotContains(t, warnings, api.MessageWarning{Kind: api.MessageWarningKindTooManyLinks})
		assert.Equal(t, body, RewriteLinks(body, 42, warnings))
	}
}

// A standards-mode table closes the paragraph and its anchor before the table's
// text. Parsing in quirks mode incorrectly includes " more" in the link text.
func TestLinksStandardsMode(t *testing.T) {
	body := `<a>skip</a><p><a href="https://evil.example/">paypal.com<table><tr><td> more</td></tr></table></a></p><a href="https://evil.example/next">example.com</a>`
	links := Links(body)
	require.Len(t, links, 3)
	assert.Equal(t, Link{Index: 1, Text: "paypal.com", Href: "https://evil.example/"}, links[1])
	warnings := ComputeWithLinks(model.DBMessage{FolderID: 1}, nil, Context{}, links)
	result := RewriteLinks(body, 42, warnings)
	assert.Contains(t, result, `href="../../../../#/link/42/1"`)
	assert.Contains(t, result, `href="../../../../#/link/42/2"`)
	rendered := Links(result)
	require.Len(t, rendered, 3)
	assert.Equal(t, "paypal.com", rendered[1].Text)
	assert.Equal(t, "example.com", rendered[2].Text)
	assert.NotContains(t, result, "https://evil.example/")
	assert.NotContains(t, result, "<!DOCTYPE")
}
