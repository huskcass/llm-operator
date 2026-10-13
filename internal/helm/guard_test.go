package helm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const clusterScoped = "cluster-scoped"

func TestNamespaceGuard(t *testing.T) {
	g := namespaceGuard{namespace: "models", mapper: testMapper()}
	for name, tc := range map[string]struct {
		manifest, refused string
	}{
		"own namespace":     {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: models\n", ""},
		"no namespace":      {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n", ""},
		"comments only":     {"# Source: x/templates/empty.yaml\n---\n", ""},
		"another namespace": {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n  namespace: kube-system\n", "into namespace kube-system"},
		clusterScoped:       {"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: a\n", "ClusterRole \"a\", which is cluster-scoped"},
		"namespace object":  {"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: a\n", clusterScoped},
		"inside a list": {"apiVersion: v1\nkind: List\nitems:\n- apiVersion: v1\n  kind: ConfigMap\n  metadata:\n    name: a\n" +
			"- apiVersion: rbac.authorization.k8s.io/v1\n  kind: Role\n  metadata:\n    name: b\n    namespace: kube-system\n", "Role \"b\" into namespace kube-system"},
		"after a good one": {"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: a\n---\napiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: b\n", clusterScoped},
		"unknown kind":     {"apiVersion: example.com/v1\nkind: Widget\nmetadata:\n  name: a\n", "cannot be told"},
		"no kind":          {"replicaCount: 1\n", "an object with no kind"},
	} {
		out, err := g.Run(bytes.NewBufferString(tc.manifest))
		switch {
		case tc.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", name, err)
		case tc.refused == "" && out.String() != tc.manifest:
			t.Errorf("%s: manifests changed:\n%s", name, out)
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused)):
			t.Errorf("%s: got %v, want an error with %q", name, err, tc.refused)
		}
	}
}

// Install and Upgrade run the guard, hooks included, and store nothing a
// refused chart rendered.
func TestApplyRefusesAnotherNamespace(t *testing.T) {
	for name, tpl := range map[string]string{
		"template": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: escape\n  namespace: kube-system\n",
		"hook": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: escape\n  namespace: kube-system\n" +
			"  annotations:\n    helm.sh/hook: pre-install,pre-upgrade\n",
	} {
		dir := writeTinyChart(t)
		if err := os.WriteFile(filepath.Join(dir, "templates", "escape.yaml"), []byte(tpl), 0o644); err != nil {
			t.Fatal(err)
		}
		c, _ := testClient(t, dir)
		ref := ChartRef{Repo: testRepo, Name: tinyChartName, Version: tinyVersion}
		if _, err := c.Install(t.Context(), testNamespace, tinyChartName, ref, nil, ApplyOpts{}); err == nil || !strings.Contains(err.Error(), "kube-system") {
			t.Errorf("%s: install: got %v, want a refusal", name, err)
		}
		if got, err := c.Get(t.Context(), testNamespace, tinyChartName); err != nil || got != nil {
			t.Errorf("%s: a refused install stored %#v, %v", name, got, err)
		}
	}

	// A chart that changes to write elsewhere is refused on upgrade.
	dir := writeTinyChart(t)
	c, _ := testClient(t, dir)
	ref := ChartRef{Repo: testRepo, Name: tinyChartName, Version: tinyVersion}
	if _, err := c.Install(t.Context(), testNamespace, tinyChartName, ref, nil, ApplyOpts{}); err != nil {
		t.Fatal(err)
	}
	cluster := "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: escape\n"
	if err := os.WriteFile(filepath.Join(dir, "templates", "escape.yaml"), []byte(cluster), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upgrade(t.Context(), testNamespace, tinyChartName, ref, nil, ApplyOpts{}); err == nil || !strings.Contains(err.Error(), clusterScoped) {
		t.Errorf("upgrade: got %v, want a refusal", err)
	}
}
