/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sample is the document shape under testdata/. The happy-path files are the
// cases; the other directories are documents this loader must reject.
type sample struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		Value int       `json:"value"`
		Wait  Duration  `json:"wait,omitempty"`
		Maybe *Duration `json:"maybe,omitempty"`
		Empty *Duration `json:"empty,omitempty"`
	} `json:"inputs"`
	Expected struct {
		Value int `json:"value"`
	} `json:"expected"`
}

func TestLoadOrdersByRelativePath(t *testing.T) {
	files, err := LoadFiles[sample]("testdata/ok")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("got %d files", len(files))
	}
	if files[0].Rel != "a/c.yaml" || files[1].Rel != "b.yaml" {
		t.Fatalf("order = %q, %q", files[0].Rel, files[1].Rel)
	}
	if files[0].Case.Inputs.Value != 1 || files[1].Case.Expected.Value != 2 {
		t.Fatalf("decoded %+v %+v", files[0].Case, files[1].Case)
	}
	if files[0].Case.Inputs.Wait.Std() != 30*time.Second {
		t.Fatalf("wait = %s", files[0].Case.Inputs.Wait.Std())
	}
	if files[0].Case.Inputs.Maybe == nil || files[0].Case.Inputs.Maybe.Std() != time.Second {
		t.Fatalf("maybe = %v", files[0].Case.Inputs.Maybe)
	}
	if files[0].Case.Inputs.Empty != nil {
		t.Fatal("null duration decoded as set")
	}

	got, err := Load[sample]("testdata/ok")
	if err != nil || len(got) != 2 || got[0].Description != "first" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
}

func TestRunUsesDescription(t *testing.T) {
	var seen []string
	Run(t, "testdata/ok", func(t *testing.T, tc sample) {
		seen = append(seen, tc.Description)
		if !strings.HasSuffix(t.Name(), "/"+tc.Description) {
			t.Errorf("subtest %q does not end with %q", t.Name(), tc.Description)
		}
	})
	if strings.Join(seen, ",") != "first,second" {
		t.Fatalf("ran %v", seen)
	}
}

func TestRejectsBadDocuments(t *testing.T) {
	checks := []struct {
		dir  string
		want string
	}{
		{"testdata/unknown-field", "unknown field"},
		{"testdata/name-mismatch", "must equal the file stem"},
		{"testdata/bad-yaml", "decode"},
		{"testdata/no-yaml", "has no yaml files"},
		{"testdata/missing", "read"},
	}
	for _, tc := range checks {
		t.Run(tc.dir, func(t *testing.T) {
			_, err := Load[sample](tc.dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

// An empty directory cannot be stored, so this one failure mode is built in a
// temp dir. The other failures live as files under testdata/.
func TestEmptyDirFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load[sample](dir); err == nil || !strings.Contains(err.Error(), "has no yaml files") {
		t.Fatalf("got %v", err)
	}
}

func TestMustLoadPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustLoad returned")
		}
	}()
	MustLoad[sample](filepath.Join(t.TempDir(), "absent"))
}

func TestRejectsDocumentsThatAreNotOneMapping(t *testing.T) {
	checks := []struct {
		name string
		body string
		want string
	}{
		{"empty.yaml", "", "no document"},
		{"comment.yaml", "# nothing to run\n", "no document"},
		{"blank.yaml", "\n\n", "no document"},
		{"null.yaml", "null\n", "document is null"},
		{"tilde.yaml", "~\n", "document is null"},
		{"sep.yaml", "---\n", "document is null"},
		{"seq.yaml", "[]\n", "must be a mapping"},
		{"multi.yaml", "name: multi\ninputs:\n  value: 1\nexpected:\n  value: 1\n---\nother: 2\n", "2 documents"},
		{"later.yaml", "name: later\ninputs:\n  value: 1\nexpected:\n  value: 1\n---\nbogus: 2\n", "2 documents"},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeYAML(t, tc.name, tc.body)
			_, err := Load[sample](dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("got %v, want it to name %s and contain %q", err, tc.name, tc.want)
			}
		})
	}
}

func TestLeadingSeparatorIsOneDocument(t *testing.T) {
	dir := writeYAML(t, "lead.yaml", "---\nname: lead\ninputs:\n  value: 3\nexpected:\n  value: 3\n")
	got, err := Load[sample](dir)
	if err != nil || len(got) != 1 || got[0].Inputs.Value != 3 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestEmptyMappingIsOneDocument(t *testing.T) {
	dir := writeYAML(t, "empty-map.yaml", "{}\n")
	got, err := Load[sample](dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestRejectsYMLExtension(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "kept.yaml"), []byte("name: kept\ninputs:\n  value: 1\nexpected:\n  value: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skipped.yml"), []byte("name: skipped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load[sample](dir)
	if err == nil || !strings.Contains(err.Error(), "skipped.yml") || !strings.Contains(err.Error(), ".yml") {
		t.Fatalf("got %v", err)
	}
}

func TestRejectsCaseMismatchedKey(t *testing.T) {
	body := "name: case\ninputs:\n  Value: 1\nexpected:\n  value: 1\n"
	dir := writeYAML(t, "case.yaml", body)
	_, err := Load[sample](dir)
	if err == nil || !strings.Contains(err.Error(), "case.yaml") || !strings.Contains(err.Error(), "Value") {
		t.Fatalf("got %v", err)
	}
}

func TestRejectsCaseMismatchInSliceAndMap(t *testing.T) {
	dir := writeYAML(t, "slice.yaml", "items:\n  - Kind: pod\n")
	if _, err := Load[nest](dir); err == nil || !strings.Contains(err.Error(), "Kind") {
		t.Fatalf("slice: %v", err)
	}
	dir = writeYAML(t, "map.yaml", "bags:\n  x:\n    N: 1\n")
	if _, err := Load[nest](dir); err == nil || !strings.Contains(err.Error(), "N") {
		t.Fatalf("map: %v", err)
	}
	// "n" is quoted: an unquoted n is a YAML 1.1 boolean, not a key.
	dir = writeYAML(t, "ok.yaml", "items:\n  - kind: pod\nbags:\n  x:\n    \"n\": 1\n")
	got, err := Load[nest](dir)
	if err != nil || len(got) != 1 || got[0].Items[0].Kind != "pod" || got[0].Bags["x"].N != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestNonPointerDurationRejectsNull(t *testing.T) {
	dir := writeYAML(t, "wait.yaml", "name: wait\ninputs:\n  value: 1\n  wait: null\nexpected:\n  value: 1\n")
	_, err := Load[sample](dir)
	if err == nil || !strings.Contains(err.Error(), "wait.yaml") || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("got %v", err)
	}
	dir = writeYAML(t, "zero.yaml", "name: zero\ninputs:\n  value: 1\n  wait: \"0s\"\nexpected:\n  value: 1\n")
	got, err := Load[sample](dir)
	if err != nil || got[0].Inputs.Wait.Std() != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
	dir = writeYAML(t, "bare.yaml", "name: bare\ninputs:\n  value: 1\n  wait: 0\nexpected:\n  value: 1\n")
	if _, err := Load[sample](dir); err == nil {
		t.Fatal("bare 0 decoded as a duration")
	}
}

func TestPromotedEmbeddedFieldsAreCaseSensitive(t *testing.T) {
	dir := writeYAML(t, "ok.yaml", "a: 1\nb: 2\n")
	got, err := Load[outer](dir)
	if err != nil || got[0].A != 1 || got[0].B != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	dir = writeYAML(t, "bad.yaml", "A: 1\nb: 2\n")
	if _, err := Load[outer](dir); err == nil || !strings.Contains(err.Error(), "A") {
		t.Fatalf("got %v", err)
	}
}

type nest struct {
	Items []struct {
		Kind string `json:"kind"`
	} `json:"items"`
	Bags map[string]struct {
		N int `json:"n"`
	} `json:"bags"`
}

type inner struct {
	A int `json:"a"`
}

type outer struct {
	inner
	B int `json:"b"`
}

func writeYAML(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestUnreadableFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.yaml")
	if err := os.WriteFile(path, []byte("name: gone\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	// Root ignores mode bits. Skip when the file is still readable.
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("process can read a mode-0 file")
	}
	if _, err := Load[sample](dir); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("got %v", err)
	}
}
