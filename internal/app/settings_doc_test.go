package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/thschue/goldie/internal/api"
	"github.com/thschue/goldie/internal/platform"
)

// The GOLDIE_API_TOKEN description in the settings registry, and so the
// generated configuration reference, states the minimum CheckToken enforces.
// platform cannot import api, so this is where the two are held together.
func TestAPITokenSettingStatesMinimumLength(t *testing.T) {
	want := fmt.Sprintf("at least %d bytes", api.MinTokenLength)
	for _, s := range platform.Settings {
		if s.Name != platform.EnvAPIToken {
			continue
		}
		if !strings.Contains(s.Meaning, want) {
			t.Errorf("%s description %q does not state %q", s.Name, s.Meaning, want)
		}
		return
	}
	t.Fatalf("%s is not in platform.Settings", platform.EnvAPIToken)
}
