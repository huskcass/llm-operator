package serving

import "testing"

const modelsphereOCI = "oci://ghcr.io/modelsphere"

func TestChartRepoAllowed(t *testing.T) {
	allowed := []string{modelsphereOCI, "https://modelsphere.github.io/helm-charts/"}
	for repo, want := range map[string]bool{
		modelsphereOCI:                              true,
		"oci://ghcr.io/modelsphere/charts":          true,
		"oci://ghcr.io/modelsphere/charts/":         true,
		"https://modelsphere.github.io/helm-charts": true,
		"oci://ghcr.io/modelsphere-x/charts":        false,
		"oci://ghcr.io/other/charts":                false,
		"https://modelsphere.github.io":             false,
		"":                                          false,
	} {
		if got := chartRepoAllowed(allowed, repo); got != want {
			t.Errorf("%q: got %v, want %v", repo, got, want)
		}
	}
	if !chartRepoAllowed(nil, "https://anything.example.com/charts") {
		t.Error("no allowlist allows any repository")
	}
}
