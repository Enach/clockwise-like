package api

import (
	"testing"

	"github.com/Enach/paceday/backend/storage"
)

// Test files are out of scope.
func TestIgnored(t *testing.T) {
	_ = storage.Save(nil, "t")
}
