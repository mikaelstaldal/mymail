package repository

import (
	"github.com/mikaelstaldal/go-server-common/devicegrant"
	"github.com/stretchr/testify/assert"
	"testing"
)

// A shared schema change requires an explicit app migration, not silently changing history.
func TestDeviceSchemaMatchesSharedVersion(t *testing.T) {
	assert.Equal(t, devicegrant.DDL, deviceSchemaV7)
}
