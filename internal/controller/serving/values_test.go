package serving

import (
	"reflect"
	"strings"
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// mergeCase is one mergeLayers call and the values it must produce.
// JSON numbers in a layer are float64, and checks compare with the same type.
type mergeCase struct {
	Name        string       `json:"name,omitempty"`
	Description string       `json:"description,omitempty"`
	Layers      []layerSpec  `json:"layers"`
	Checks      []mergeCheck `json:"checks"`
}

type mergeCheck struct {
	Path   []string `json:"path"`
	Absent bool     `json:"absent,omitempty"`
	Equals any      `json:"equals,omitempty"`
}

func TestMergeLayers(t *testing.T) {
	cases.Run(t, "testdata/values/merge", func(t *testing.T, tc mergeCase) {
		got, err := mergeLayers(layersFrom(tc.Layers))
		if err != nil {
			t.Fatal(err)
		}
		for _, check := range tc.Checks {
			val, ok := lookupPath(got, check.Path)
			if check.Absent {
				if ok {
					t.Errorf("%s: %v was kept: %#v", tc.Description, check.Path, val)
				}
				continue
			}
			if !ok || !reflect.DeepEqual(val, check.Equals) {
				t.Errorf("%s: %v = %#v, want %#v", tc.Description, check.Path, val, check.Equals)
			}
		}
	})
}

func lookupPath(root map[string]any, path []string) (any, bool) {
	var cur any = root
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[p]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// hashCase runs ordered appliedHash checks. The stable check records the
// digest the later checks compare against, so it has to come first.
type hashCase struct {
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	Checks      []hashCheck `json:"checks"`
}

type hashCheck struct {
	Kind    string         `json:"kind"`
	Chart   string         `json:"chart,omitempty"`
	Version string         `json:"version,omitempty"`
	Left    map[string]any `json:"left,omitempty"`
	Right   map[string]any `json:"right,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
	Prefix  string         `json:"prefix,omitempty"`
	HashLen int            `json:"hashLen,omitempty"`
}

func TestAppliedHashIsStableAndSensitive(t *testing.T) {
	cases.Run(t, "testdata/values/hash", func(t *testing.T, tc hashCase) {
		var base string
		for _, check := range tc.Checks {
			switch check.Kind {
			case "stable":
				a, err := appliedHash(check.Chart, check.Version, check.Left)
				if err != nil {
					t.Fatal(err)
				}
				b, err := appliedHash(check.Chart, check.Version, check.Right)
				if err != nil {
					t.Fatal(err)
				}
				if a != b || !strings.HasPrefix(a, check.Prefix) || len(a) != check.HashLen {
					t.Fatalf("hash = %s and %s", a, b)
				}
				base = a
			case "nilEqualsEmpty":
				same, err := jsonEqual(nil, map[string]any{})
				if err != nil || !same {
					t.Fatalf("nil and empty map: %v %v", same, err)
				}
			case "version":
				if base == "" {
					t.Fatal("stable hash was not recorded")
				}
				changed, err := appliedHash(check.Chart, check.Version, check.Values)
				if err != nil || changed == base {
					t.Fatalf("version change did not move the hash: %s", changed)
				}
			case "values":
				if base == "" {
					t.Fatal("stable hash was not recorded")
				}
				changed, err := appliedHash(check.Chart, check.Version, check.Values)
				if err != nil || changed == base {
					t.Fatalf("values change did not move the hash: %s", changed)
				}
			default:
				t.Fatalf("%s: unknown check %q", tc.Name, check.Kind)
			}
		}
	})
}
