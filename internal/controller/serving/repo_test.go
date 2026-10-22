package serving

import (
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// repoCase is one chartRepoAllowed input. NilAllowlist passes a nil allowlist,
// which permits every repository. Otherwise Allowed is the allowlist.
type repoCase struct {
	Name         string   `json:"name,omitempty"`
	Description  string   `json:"description,omitempty"`
	Allowed      []string `json:"allowed,omitempty"`
	NilAllowlist bool     `json:"nilAllowlist,omitempty"`
	Repo         string   `json:"repo"`
	Want         bool     `json:"want"`
}

func TestChartRepoAllowed(t *testing.T) {
	cases.Run(t, "testdata/repo", func(t *testing.T, tc repoCase) {
		var allowed []string
		if !tc.NilAllowlist {
			allowed = tc.Allowed
		}
		if got := chartRepoAllowed(allowed, tc.Repo); got != tc.Want {
			t.Errorf("%s: %q: got %v, want %v", tc.Description, tc.Repo, got, tc.Want)
		}
	})
}
