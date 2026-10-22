package serving

import (
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// repoCase is one chartRepoAllowed input.
// allowed is the allowlist and must be set, possibly as an empty list.
// An empty list and a nil list both permit every repository; nilAllowlist
// is how a case passes nil explicitly. Omitting both is an error, because
// a forgotten allowlist would otherwise permit every repository.
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
		if !tc.NilAllowlist && tc.Allowed == nil {
			t.Fatal("set allowed (an empty list allows every repository) or nilAllowlist")
		}
		var allowed []string
		if !tc.NilAllowlist {
			allowed = tc.Allowed
		}
		if got := chartRepoAllowed(allowed, tc.Repo); got != tc.Want {
			t.Errorf("%s: %q: got %v, want %v", tc.Description, tc.Repo, got, tc.Want)
		}
	})
}
