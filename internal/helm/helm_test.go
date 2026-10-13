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
)

const (
	tinyVersion    = "0.1.0"
	tinyChartName  = "tiny"
	testNamespace  = "default"
	testReleaseUID = "uid-1"
	uidLabelKey    = "serving.modelsphere.dev/llmservice-uid"
	nameLabelKey   = "serving.modelsphere.dev/llmservice"
	testRepo       = "oci://example.com/charts"
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

func TestGetInstallUpgradeUninstall(t *testing.T) {
	const valueOne = "one"
	c, _ := testClient(t, writeTinyChart(t))
	ctx := t.Context()
	const ns, name = testNamespace, tinyChartName
	ref := ChartRef{Repo: testRepo, Name: name, Version: tinyVersion}
	labels := testLabels(name)
	before := time.Now().Add(-time.Second)

	got, err := c.Get(ctx, ns, name)
	if err != nil || got != nil {
		t.Fatalf("absent get = %#v, %v", got, err)
	}

	installed, err := c.Install(ctx, ns, name, ref, map[string]any{"v": valueOne}, ApplyOpts{ForceConflicts: true, Labels: labels})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if installed.Revision != 1 || installed.Status != "deployed" || installed.ChartName != name || installed.ChartVersion != tinyVersion {
		t.Fatalf("install release = %#v", installed)
	}
	if installed.Config["v"] != valueOne {
		t.Fatalf("install config = %#v", installed.Config)
	}
	if installed.Labels[uidLabelKey] != testReleaseUID || installed.Updated.Before(before) {
		t.Fatalf("install labels/updated = %#v", installed)
	}

	got, err = c.Get(ctx, ns, name)
	if err != nil || got == nil || got.Revision != 1 || got.Config["v"] != valueOne {
		t.Fatalf("get after install = %#v, %v", got, err)
	}
	if got.Labels[uidLabelKey] != testReleaseUID || got.Updated.Before(before) {
		t.Fatalf("get labels/updated = %#v", got)
	}

	upgraded, err := c.Upgrade(ctx, ns, name, ref, map[string]any{"v": "two"}, ApplyOpts{Labels: labels})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if upgraded.Revision != 2 || upgraded.Config["v"] != "two" || upgraded.Status != "deployed" {
		t.Fatalf("upgrade release = %#v", upgraded)
	}
	if upgraded.Labels[uidLabelKey] != testReleaseUID || upgraded.Updated.Before(before) {
		t.Fatalf("upgrade labels/updated = %#v", upgraded)
	}

	if err := c.Uninstall(ctx, ns, name); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	got, err = c.Get(ctx, ns, name)
	if err != nil || got != nil {
		t.Fatalf("get after uninstall = %#v, %v", got, err)
	}
	if err := c.Uninstall(ctx, ns, name); err != nil {
		t.Fatalf("uninstall absent: %v", err)
	}
}

func TestHistoryNewestFirstAndRollbackPendingUpgrade(t *testing.T) {
	c, cfg := testClient(t, writeTinyChart(t))
	ctx := t.Context()
	const ns, name = testNamespace, tinyChartName
	labels := testLabels(name)
	deployedAt := time.Now().Add(-2 * time.Hour).UTC()
	failedAt := time.Now().Add(-time.Hour).UTC()
	pendingAt := time.Now().Add(-30 * time.Minute).UTC()
	storeRelease(t, cfg, storedRelease(name, 1, common.StatusDeployed, deployedAt, labels))
	storeRelease(t, cfg, storedRelease(name, 2, common.StatusFailed, failedAt, labels))
	storeRelease(t, cfg, storedRelease(name, 3, common.StatusPendingUpgrade, pendingAt, labels))

	got, err := c.Get(ctx, ns, name)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Revision != 3 || got.Status != string(common.StatusPendingUpgrade) || !got.Updated.Equal(pendingAt) {
		t.Fatalf("get pending = %#v", got)
	}
	if got.Labels[uidLabelKey] != testReleaseUID {
		t.Fatalf("get labels = %#v", got.Labels)
	}

	hist, err := c.History(ctx, ns, name)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 3 || hist[0].Revision != 3 || hist[1].Revision != 2 || hist[2].Revision != 1 {
		t.Fatalf("history = %#v", hist)
	}
	if hist[0].Status != string(common.StatusPendingUpgrade) || hist[1].Status != string(common.StatusFailed) || hist[2].Status != string(common.StatusDeployed) {
		t.Fatalf("history status = %#v", hist)
	}
	if !hist[2].Updated.Equal(deployedAt) || hist[2].Labels[uidLabelKey] != testReleaseUID {
		t.Fatalf("deployed history = %#v", hist[2])
	}

	if err := c.Rollback(ctx, ns, name, 1); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got, err = c.Get(ctx, ns, name)
	if err != nil {
		t.Fatalf("get after rollback: %v", err)
	}
	if got.Status != string(common.StatusDeployed) || got.Revision != 4 || got.Labels[uidLabelKey] != testReleaseUID || got.ChartName != name {
		t.Fatalf("rolled back = %#v", got)
	}
}

func TestPendingInstallFailedAndAbsentHistory(t *testing.T) {
	c, cfg := testClient(t, writeTinyChart(t))
	ctx := t.Context()
	const ns = testNamespace
	labels := testLabels("fresh")
	failedAt := time.Now().Add(-time.Hour).UTC()
	installAt := time.Now().Add(-3 * time.Hour).UTC()
	storeRelease(t, cfg, storedRelease("fresh", 1, common.StatusPendingInstall, installAt, labels))
	storeRelease(t, cfg, storedRelease("broke", 1, common.StatusFailed, failedAt, nil))

	hist, err := c.History(ctx, ns, "fresh")
	if err != nil || len(hist) != 1 || hist[0].Status != string(common.StatusPendingInstall) || !hist[0].Updated.Equal(installAt) {
		t.Fatalf("pending-install history = %#v, %v", hist, err)
	}
	if err := c.Uninstall(ctx, ns, "fresh"); err != nil {
		t.Fatalf("uninstall pending-install: %v", err)
	}
	got, err := c.Get(ctx, ns, "fresh")
	if err != nil || got != nil {
		t.Fatalf("get after uninstall pending-install = %#v, %v", got, err)
	}

	got, err = c.Get(ctx, ns, "broke")
	if err != nil || got == nil || got.Status != string(common.StatusFailed) || !got.Updated.Equal(failedAt) {
		t.Fatalf("failed get = %#v, %v", got, err)
	}
	hist, err = c.History(ctx, ns, "broke")
	if err != nil || len(hist) != 1 || hist[0].Status != string(common.StatusFailed) || hist[0].Revision != 1 {
		t.Fatalf("failed history = %#v, %v", hist, err)
	}

	missing, err := c.History(ctx, ns, "absent")
	if err != nil || missing != nil {
		t.Fatalf("absent history = %#v, %v", missing, err)
	}
}

func TestRollbackPendingRollback(t *testing.T) {
	c, cfg := testClient(t, writeTinyChart(t))
	ctx := t.Context()
	const ns, name = testNamespace, "back"
	deployedAt := time.Now().Add(-2 * time.Hour).UTC()
	pendingAt := time.Now().Add(-30 * time.Minute).UTC()
	storeRelease(t, cfg, storedRelease(name, 1, common.StatusDeployed, deployedAt, testLabels(name)))
	storeRelease(t, cfg, storedRelease(name, 2, common.StatusPendingRollback, pendingAt, testLabels(name)))

	if err := c.Rollback(ctx, ns, name, 1); err != nil {
		t.Fatalf("rollback pending-rollback: %v", err)
	}
	got, err := c.Get(ctx, ns, name)
	if err != nil || got == nil || got.Status != string(common.StatusDeployed) || got.Revision != 3 {
		t.Fatalf("pending-rollback recovered = %#v, %v", got, err)
	}
}

func testLabels(name string) map[string]string {
	return map[string]string{
		uidLabelKey:  testReleaseUID,
		nameLabelKey: name,
	}
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
