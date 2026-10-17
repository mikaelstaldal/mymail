package phishing

import (
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
