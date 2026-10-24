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
