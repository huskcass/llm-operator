package helm

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

const (
	tinyVersion   = "0.1.0"
	tinyChartName = "tiny"
	testNamespace = "default"
	testRepo      = "oci://example.com/charts"
)

func testClient(t *testing.T, chartDir string) (*Client, *action.Configuration) {
	t.Helper()
	mem := driver.NewMemory()
	cfg := action.NewConfiguration()
	cfg.Releases = storage.Init(mem)
	cfg.KubeClient = &kubefake.FailingKubeClient{
		PrintingKubeClient: kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard},
	}
	cfg.Capabilities = chartcommon.DefaultCapabilities
	return &Client{
		cacheDir: t.TempDir(),
		mapper:   testMapper(),
		load: func(context.Context, ChartRef, *Credentials) (*chartv2.Chart, error) {
			return loader.Load(chartDir)
		},
		newConfig: func(ns string) (*action.Configuration, error) {
			mem.SetNamespace(ns)
			return cfg, nil
		},
	}, cfg
}

// testMapper knows the kinds the tests render, as a cluster's discovery would.
func testMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper(nil)
	m.Add(schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}, meta.RESTScopeNamespace)
	m.Add(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"}, meta.RESTScopeRoot)
	m.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "Role"}, meta.RESTScopeNamespace)
	m.Add(schema.GroupVersionKind{Group: "rbac.authorization.k8s.io", Version: "v1", Kind: "ClusterRole"}, meta.RESTScopeRoot)
	return m
}

func writeTinyChart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	chartYAML := "apiVersion: v2\nname: tiny\nversion: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(chartYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tpl := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  v: {{ .Values.v | quote }}\n"
	if err := os.WriteFile(filepath.Join(dir, "templates", "cm.yaml"), []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func storeRelease(t *testing.T, cfg *action.Configuration, rel *releasev1.Release) {
	t.Helper()
	if err := cfg.Releases.Create(rel); err != nil {
		t.Fatal(err)
	}
}

func storedRelease(name string, rev int, status common.Status, updated time.Time, labels map[string]string) *releasev1.Release {
	return &releasev1.Release{
		Name:      name,
		Namespace: testNamespace,
		Version:   rev,
		Labels:    labels,
		Config:    map[string]any{"v": "one"},
		Manifest:  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + tinyChartName + "\n",
		Chart:     &chartv2.Chart{Metadata: &chartv2.Metadata{Name: tinyChartName, Version: tinyVersion}},
		Info: &releasev1.Info{
			Status:        status,
			LastDeployed:  updated,
			FirstDeployed: updated,
		},
	}
}

// clientCase is one client sequence. Seed times are offsets from a base
// captured at the start, stored as base.Add(offset).UTC().
// updatedAfterStart mirrors !Updated.Before(base.Add(-time.Second)).
type clientCase struct {
	Name         string        `json:"name,omitempty"`
	Description  string        `json:"description,omitempty"`
	Namespace    string        `json:"namespace,omitempty"`
	ChartRepo    string        `json:"chartRepo,omitempty"`
	ChartName    string        `json:"chartName,omitempty"`
	ChartVersion string        `json:"chartVersion,omitempty"`
	Seed         []seedRelease `json:"seed,omitempty"`
	Steps        []clientStep  `json:"steps"`
}

type seedRelease struct {
	Name     string            `json:"name"`
	Revision int               `json:"revision"`
	Status   string            `json:"status"`
	Updated  cases.Duration    `json:"updated"`
	Labels   map[string]string `json:"labels,omitempty"`
}

type clientStep struct {
	Op             string            `json:"op"`
	Release        string            `json:"release"`
	Values         map[string]any    `json:"values,omitempty"`
	ForceConflicts bool              `json:"forceConflicts,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	Revision       int               `json:"revision,omitempty"`
	Absent         bool              `json:"absent,omitempty"`
	Expect         *releaseExpect    `json:"expect,omitempty"`
	History        []releaseExpect   `json:"history,omitempty"`
}

type releaseExpect struct {
	Revision          *int              `json:"revision,omitempty"`
	Status            string            `json:"status,omitempty"`
	ChartName         string            `json:"chartName,omitempty"`
	ChartVersion      string            `json:"chartVersion,omitempty"`
	Config            map[string]string `json:"config,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Updated           *cases.Duration   `json:"updated,omitempty"`
	UpdatedAfterStart bool              `json:"updatedAfterStart,omitempty"`
}

func TestGetInstallUpgradeUninstall(t *testing.T) {
	cases.Run(t, "testdata/client/get-install-upgrade-uninstall", runClientCase)
}

func TestHistoryNewestFirstAndRollbackPendingUpgrade(t *testing.T) {
	cases.Run(t, "testdata/client/history-newest-first", runClientCase)
}

func TestPendingInstallFailedAndAbsentHistory(t *testing.T) {
	cases.Run(t, "testdata/client/pending-install-failed", runClientCase)
}

func TestRollbackPendingRollback(t *testing.T) {
	cases.Run(t, "testdata/client/rollback-pending-rollback", runClientCase)
}

func runClientCase(t *testing.T, tc clientCase) {
	t.Helper()
	base := time.Now()
	ns := tc.Namespace
	if ns == "" {
		ns = testNamespace
	}
	c, cfg := testClient(t, writeTinyChart(t))
	for _, seed := range tc.Seed {
		at := base.Add(seed.Updated.Std()).UTC()
		rel := storedRelease(seed.Name, seed.Revision, common.Status(seed.Status), at, seed.Labels)
		storeRelease(t, cfg, rel)
	}
	ref := ChartRef{Repo: tc.ChartRepo, Name: tc.ChartName, Version: tc.ChartVersion}
	if ref.Repo == "" {
		ref.Repo = testRepo
	}
	if ref.Name == "" {
		ref.Name = tinyChartName
	}
	if ref.Version == "" {
		ref.Version = tinyVersion
	}
	ctx := t.Context()
	for _, step := range tc.Steps {
		runClientStep(t, ctx, c, ns, ref, base, step)
	}
}

func runClientStep(t *testing.T, ctx context.Context, c *Client, ns string, ref ChartRef, base time.Time, step clientStep) {
	t.Helper()
	if step.Release == "" {
		t.Fatal("step has no release")
	}
	switch step.Op {
	case "get":
		got, err := c.Get(ctx, ns, step.Release)
		if step.Absent {
			if err != nil || got != nil {
				t.Fatalf("absent get = %#v, %v", got, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		checkRelease(t, "get", got, step.Expect, base)
	case "install":
		got, err := c.Install(ctx, ns, step.Release, ref, step.Values, ApplyOpts{
			ForceConflicts: step.ForceConflicts,
			Labels:         step.Labels,
		})
		if err != nil {
			t.Fatalf("install: %v", err)
		}
		checkRelease(t, "install", got, step.Expect, base)
	case "upgrade":
		got, err := c.Upgrade(ctx, ns, step.Release, ref, step.Values, ApplyOpts{
			ForceConflicts: step.ForceConflicts,
			Labels:         step.Labels,
		})
		if err != nil {
			t.Fatalf("upgrade: %v", err)
		}
		checkRelease(t, "upgrade", got, step.Expect, base)
	case "uninstall":
		if err := c.Uninstall(ctx, ns, step.Release); err != nil {
			t.Fatalf("uninstall: %v", err)
		}
	case "history":
		hist, err := c.History(ctx, ns, step.Release)
		if err != nil {
			t.Fatalf("history: %v", err)
		}
		if step.Absent {
			if hist != nil {
				t.Fatalf("absent history = %#v, %v", hist, err)
			}
			return
		}
		if len(hist) != len(step.History) {
			t.Fatalf("history = %#v", hist)
		}
		for i := range step.History {
			checkRelease(t, "history", &hist[i], &step.History[i], base)
		}
	case "rollback":
		if step.Revision == 0 {
			t.Fatal("rollback has no revision")
		}
		if err := c.Rollback(ctx, ns, step.Release, step.Revision); err != nil {
			t.Fatalf("rollback: %v", err)
		}
	default:
		t.Fatalf("op %q", step.Op)
	}
}

func checkRelease(t *testing.T, what string, rel *Release, exp *releaseExpect, base time.Time) {
	t.Helper()
	if rel == nil {
		t.Fatalf("%s = nil", what)
	}
	if exp == nil {
		t.Fatalf("%s has no expect", what)
	}
	if exp.Revision != nil && rel.Revision != *exp.Revision {
		t.Fatalf("%s revision = %d, want %d", what, rel.Revision, *exp.Revision)
	}
	if exp.Status != "" && rel.Status != exp.Status {
		t.Fatalf("%s status = %q, want %q", what, rel.Status, exp.Status)
	}
	if exp.ChartName != "" && rel.ChartName != exp.ChartName {
		t.Fatalf("%s chart = %q, want %q", what, rel.ChartName, exp.ChartName)
	}
	if exp.ChartVersion != "" && rel.ChartVersion != exp.ChartVersion {
		t.Fatalf("%s chart version = %q, want %q", what, rel.ChartVersion, exp.ChartVersion)
	}
	for k, want := range exp.Config {
		got, ok := rel.Config[k].(string)
		if !ok || got != want {
			t.Fatalf("%s config[%s] = %#v, want %q", what, k, rel.Config[k], want)
		}
	}
	for k, want := range exp.Labels {
		if rel.Labels[k] != want {
			t.Fatalf("%s label %s = %q, want %q", what, k, rel.Labels[k], want)
		}
	}
	if exp.Updated != nil {
		want := base.Add(exp.Updated.Std()).UTC()
		if !rel.Updated.Equal(want) {
			t.Fatalf("%s updated = %s, want %s", what, rel.Updated, want)
		}
	}
	if exp.UpdatedAfterStart && rel.Updated.Before(base.Add(-time.Second)) {
		t.Fatalf("%s updated = %s, want after %s", what, rel.Updated, base.Add(-time.Second))
	}
}
