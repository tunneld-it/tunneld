package pid

import (
	"errors"
	"testing"
)

// TestBackground pins -d on Windows: unsupported, starting nothing.
func TestBackground(t *testing.T) {
	if _, err := Background("tunneld", nil, nil); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Background = %v, want ErrUnsupported", err)
	}
}
