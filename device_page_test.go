package main

import (
	commonweb "github.com/mikaelstaldal/go-server-common/web"
	"github.com/mikaelstaldal/mymail/internal/token"
	"github.com/mikaelstaldal/mymail/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"testing/fstest"
)

func TestDevicePageUsesAllowedImportMap(t *testing.T) {
	allowed, err := commonweb.ImportMapCSPHash(web.Static)
	require.NoError(t, err)
	page, err := web.Static.ReadFile("static/device-approval.html")
	require.NoError(t, err)
	hash, err := commonweb.ImportMapCSPHash(fstest.MapFS{"static/index.html": {Data: page}})
	require.NoError(t, err)
	assert.Equal(t, allowed, hash, "dedicated page must match the importmap allowed by server CSP")
	assert.Contains(t, string(page), `src="device-approval.js"`)
	assert.NotContains(t, string(page), `src="app.js"`)
}
func TestDeviceVerificationLinkPreservesDeploymentPrefix(t *testing.T) {
	assert.Equal(t, "https://example.com/mymail/device-approval.html", token.DeviceVerificationURI("https://example.com/mymail/", "127.0.0.1", 8080))
	assert.Equal(t, "http://127.0.0.1:8080/device-approval.html", token.DeviceVerificationURI("", "127.0.0.1", 8080))
}
