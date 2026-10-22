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

func TestEntriesFollowsDescription(t *testing.T) {
	entries := Entries("testdata/ok", func(s sample) string { return s.Description })
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	entries = Entries[sample]("testdata/ok", nil)
	if len(entries) != 2 {
		t.Fatalf("nil description: got %d entries", len(entries))
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
