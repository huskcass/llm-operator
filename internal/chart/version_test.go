package chart

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

func spec(t *testing.T, s string) Spec {
	t.Helper()
	sp, err := ParseSpec(s)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// bareCase is one exact-version check. Exact is a pointer so a range can
// expect the empty string and an omitted field stays distinct from that.
type bareCase struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Spec        string   `json:"spec"`
	Exact       *string  `json:"exact,omitempty"`
	Allows      []string `json:"allows,omitempty"`
	Refuses     []string `json:"refuses,omitempty"`
}

func TestABareVersionIsExact(t *testing.T) {
	cases.Run(t, "testdata/version/bare", func(t *testing.T, tc bareCase) {
		sp := spec(t, tc.Spec)
		if tc.Exact == nil {
			t.Fatal("exact is omitted; set it to the one version or to an empty string")
		}
		if sp.Exact() != *tc.Exact {
			if *tc.Exact == "" {
				t.Error("a range names no one version")
			} else {
				t.Errorf("Exact = %q, want %s", sp.Exact(), *tc.Exact)
			}
		}
		bad := false
		for _, v := range tc.Allows {
			if !sp.Allows(v) {
				bad = true
			}
		}
		for _, v := range tc.Refuses {
			if sp.Allows(v) {
				bad = true
			}
		}
		if bad {
			t.Errorf("%s must allow %v and not %v", tc.Spec, tc.Allows, tc.Refuses)
		}
	})
}

type parseCase struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Spec        string `json:"spec"`
}

func TestParseSpecRefusesWhatIsNotAVersion(t *testing.T) {
	cases.Run(t, "testdata/version/parse", func(t *testing.T, tc parseCase) {
		if _, err := ParseSpec(tc.Spec); err == nil {
			t.Errorf("%q: want an error", tc.Spec)
		}
	})
}

type matchCase struct {
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Spec        string   `json:"spec"`
	Have        []string `json:"have"`
	Want        []string `json:"want"`
}

func TestMatchIsNewestFirstAndSkipsNonVersions(t *testing.T) {
	cases.Run(t, "testdata/version/match", func(t *testing.T, tc matchCase) {
		got := spec(t, tc.Spec).Match(tc.Have)
		if !reflect.DeepEqual(got, tc.Want) {
			t.Errorf("got %v, want %v -- %s", got, tc.Want, tc.Description)
		}
	})
}

// resolveCase is one Resolve call.
// Exactly one of noList, listNil, and list is set.
// noList fails the test if the repository is read.
// listNil passes a nil Lister.
// list, including an empty array, is returned as the catalog.
type resolveCase struct {
	Name          string    `json:"name,omitempty"`
	Description   string    `json:"description,omitempty"`
	Spec          string    `json:"spec"`
	Want          string    `json:"want,omitempty"`
	Keep          string    `json:"keep,omitempty"`
	Got           string    `json:"got,omitempty"`
	NoList        bool      `json:"noList,omitempty"`
	ListNil       bool      `json:"listNil,omitempty"`
	List          *[]string `json:"list,omitempty"`
	ErrorContains string    `json:"errorContains,omitempty"`
}

func (tc resolveCase) lister(t *testing.T) Lister {
	t.Helper()
	n := 0
	if tc.NoList {
		n++
	}
	if tc.ListNil {
		n++
	}
	if tc.List != nil {
		n++
	}
	if n != 1 {
		t.Fatal("set exactly one of noList, listNil, and list")
	}
	if tc.NoList {
		return func(context.Context) ([]string, error) {
			t.Error("the repository must not be read")
			return nil, errors.New("read")
		}
	}
	if tc.ListNil {
		return nil
	}
	vs := *tc.List
	return func(context.Context) ([]string, error) { return vs, nil }
}

func runResolve(t *testing.T, tc resolveCase) {
	t.Helper()
	if tc.ErrorContains == "" && tc.Got == "" {
		t.Fatal("set got or errorContains")
	}
	got, err := Resolve(t.Context(), spec(t, tc.Spec), tc.Want, tc.Keep, tc.lister(t))
	if tc.ErrorContains != "" {
		if err == nil || !strings.Contains(err.Error(), tc.ErrorContains) {
			t.Errorf("%s: got %q, %v; want an error with %q", tc.Description, got, err, tc.ErrorContains)
		}
		return
	}
	if err != nil || got != tc.Got {
		t.Errorf("%s: got %q, %v; want %q", tc.Description, got, err, tc.Got)
	}
}

func TestResolve(t *testing.T) {
	cases.Run(t, "testdata/version/resolve", runResolve)
}

func TestResolveRefuses(t *testing.T) {
	cases.Run(t, "testdata/version/resolve-refuses", runResolve)
}
