package helm

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// namespaceCase is one post-render. An empty refused string means the
// manifest is returned unchanged and must equal the input bytes.
type namespaceCase struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Manifest    string `json:"manifest"`
	Refused     string `json:"refused,omitempty"`
}

func TestNamespaceGuard(t *testing.T) {
	cases.Run(t, "testdata/guard/namespace", func(t *testing.T, tc namespaceCase) {
		g := namespaceGuard{namespace: "models", mapper: testMapper()}
		out, err := g.Run(bytes.NewBufferString(tc.Manifest))
		if tc.Refused == "" {
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if out == nil || out.String() != tc.Manifest {
				got := ""
				if out != nil {
					got = out.String()
				}
				t.Fatalf("manifests changed:\ngot %q\nwant %q", got, tc.Manifest)
			}
			return
		}
		if err == nil || !strings.Contains(err.Error(), tc.Refused) {
			t.Fatalf("got %v, want an error with %q", err, tc.Refused)
		}
	})
}

// applyCase is one install or upgrade sequence on a fresh client.
// The refused template and hook installs do not share the later upgrade.
type applyCase struct {
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	Steps       []applyStep `json:"steps"`
}

type applyStep struct {
	Write         string `json:"write,omitempty"`
	Op            string `json:"op,omitempty"`
	ErrorContains string `json:"errorContains,omitempty"`
	Absent        bool   `json:"absent,omitempty"`
}

func TestApplyRefusesAnotherNamespace(t *testing.T) {
	cases.Run(t, "testdata/guard/apply", runApplyCase)
}

func runApplyCase(t *testing.T, tc applyCase) {
	t.Helper()
	dir := writeTinyChart(t)
	c, _ := testClient(t, dir)
	ref := ChartRef{Repo: testRepo, Name: tinyChartName, Version: tinyVersion}
	ctx := t.Context()
	for _, step := range tc.Steps {
		if step.Write != "" {
			err := os.WriteFile(filepath.Join(dir, "templates", "escape.yaml"), []byte(step.Write), 0o644)
			if err != nil {
				t.Fatal(err)
			}
		}
		switch step.Op {
		case "":
			if step.Write == "" {
				t.Fatal("step has no write and no op")
			}
		case "install":
			_, err := c.Install(ctx, testNamespace, tinyChartName, ref, nil, ApplyOpts{})
			if step.ErrorContains == "" {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), step.ErrorContains) {
				t.Errorf("install: got %v, want a refusal", err)
			}
		case "upgrade":
			_, err := c.Upgrade(ctx, testNamespace, tinyChartName, ref, nil, ApplyOpts{})
			if step.ErrorContains == "" {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if err == nil || !strings.Contains(err.Error(), step.ErrorContains) {
				t.Errorf("upgrade: got %v, want a refusal", err)
			}
		case "get":
			got, err := c.Get(ctx, testNamespace, tinyChartName)
			if !step.Absent {
				t.Fatal("get step expects absent")
			}
			if err != nil || got != nil {
				t.Errorf("a refused install stored %#v, %v", got, err)
			}
		default:
			t.Fatalf("op %q", step.Op)
		}
	}
}
