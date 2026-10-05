package handler

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeReferences(t *testing.T) {
	assert.Equal(t, "old\nnew", normalizeReferences([]string{"<old>", "", "<new>"}))
	assert.Equal(t, "new", normalizeReferences([]string{strings.Repeat("x", maxRefsBytes), "<new>"}))
	assert.Empty(t, normalizeReferences([]string{"old", strings.Repeat("x", maxRefsBytes+1)}))
	assert.Equal(t, strings.Repeat("é", maxRefsBytes/2), normalizeReferences([]string{"<" + strings.Repeat("é", maxRefsBytes/2) + ">"}))
	assert.Equal(t, "x", normalizeReferences([]string{"\rx\n\x00"}))
}

func TestNormalizeReferencesBoundedWork(t *testing.T) {
	refs := make([]string, 20_000)
	for i := range refs {
		refs[i] = "<x@y>"
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	got := normalizeReferences(refs)
	runtime.ReadMemStats(&after)
	require.Len(t, strings.Split(got, "\n"), maxRefsCount)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
}
