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

// Package cases loads one YAML document per test case.
//
// Files are discovered recursively under a directory. Only names ending in
// ".yaml" are cases; anything else (a README, a note) is ignored. The result
// is ordered by the slash-separated path relative to that directory, so a
// rename or a new file changes order only by that path.
//
// Each file is decoded with sigs.k8s.io/yaml in strict mode: an unknown field,
// a duplicate key, or a value of the wrong type fails the load. An empty
// directory, a missing directory, and an unreadable file fail as well.
//
// A decoded case may carry Name and Description. Name, when set, must equal the
// file stem (the base name without ".yaml"). Description is the sentence
// runners use as the subtest or Ginkgo entry text; when it is empty, the
// stem is used instead.
//
// Durations in a case are Go duration strings ("30s", "1h", "-2h") decoded
// into Duration. A nil pointer is YAML null or an omitted field; a pointer
// to zero is the string "0s".
//
// Keys are decoded with YAML 1.1 rules. An unquoted n, y, yes, no, on, or off
// is a boolean, not a string, and strict decoding then reports it as an
// unknown field. Quote any such key.
package cases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"sigs.k8s.io/yaml"
)

// Duration is a time.Duration stored in YAML as a Go duration string.
type Duration time.Duration

// Std returns the duration as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalJSON accepts a JSON string in Go duration syntax.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration: %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// File is one decoded case and where it was read from.
type File[T any] struct {
	// Rel is the path relative to the loaded directory, with forward slashes.
	Rel string
	// Stem is the file name without the .yaml suffix.
	Stem string
	// Case is the decoded document.
	Case T
}

// Load reads and decodes every case file under dir.
func Load[T any](dir string) ([]T, error) {
	files, err := LoadFiles[T](dir)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(files))
	for i, f := range files {
		out[i] = f.Case
	}
	return out, nil
}

// LoadFiles is Load with the relative path and stem of each file.
func LoadFiles[T any](dir string) ([]File[T], error) {
	rels, err := yamlRels(dir)
	if err != nil {
		return nil, err
	}
	out := make([]File[T], 0, len(rels))
	for _, rel := range rels {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("cases: read %s: %w", rel, err)
		}
		var tc T
		if err := yaml.UnmarshalStrict(body, &tc); err != nil {
			return nil, fmt.Errorf("cases: decode %s: %w", rel, err)
		}
		stem := strings.TrimSuffix(filepath.Base(rel), ".yaml")
		if err := checkName(tc, stem, rel); err != nil {
			return nil, err
		}
		out = append(out, File[T]{Rel: rel, Stem: stem, Case: tc})
	}
	return out, nil
}

// MustLoad is LoadFiles for a Ginkgo container, which has no testing.T yet.
// A bad directory panics so the suite fails instead of running zero cases.
func MustLoad[T any](dir string) []File[T] {
	files, err := LoadFiles[T](dir)
	if err != nil {
		panic(err)
	}
	return files
}

// Run loads dir and calls fn as one subtest per file.
// The subtest name is the case Description when that field is set, and the
// file stem otherwise, so an existing sentence stays the name go test prints.
func Run[T any](t *testing.T, dir string, fn func(t *testing.T, tc T)) {
	t.Helper()
	files, err := LoadFiles[T](dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		t.Run(SpecName(f.Case, f.Stem), func(t *testing.T) {
			fn(t, f.Case)
		})
	}
}

// Entries loads dir as Ginkgo table entries, one Entry per file.
// description supplies the entry text; a nil description, or a result of "",
// uses the file stem. A load error panics so suite setup fails loudly.
func Entries[T any](dir string, description func(T) string) []ginkgo.TableEntry {
	files, err := LoadFiles[T](dir)
	if err != nil {
		panic(err)
	}
	entries := make([]ginkgo.TableEntry, len(files))
	for i, f := range files {
		text := f.Stem
		if description != nil {
			if d := description(f.Case); d != "" {
				text = d
			}
		}
		entries[i] = ginkgo.Entry(text, f.Case)
	}
	return entries
}

// SpecName is the subtest or entry text for a case: its Description, or stem.
func SpecName[T any](tc T, stem string) string {
	if d, ok := stringField(tc, "Description"); ok && d != "" {
		return d
	}
	return stem
}

func yamlRels(dir string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rels = append(rels, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cases: read %s: %w", dir, err)
	}
	if len(rels) == 0 {
		return nil, fmt.Errorf("cases: %s has no yaml files", dir)
	}
	slices.Sort(rels)
	return rels, nil
}

func checkName(tc any, stem, rel string) error {
	name, ok := stringField(tc, "Name")
	if !ok || name == "" {
		return nil
	}
	if name != stem {
		return fmt.Errorf("cases: %s: name %q must equal the file stem %q", rel, name, stem)
	}
	return nil
}

// stringField reads an exported string field. ok is false when the case has
// no such field, so a case type is not required to carry Name or Description.
func stringField(tc any, field string) (string, bool) {
	v := reflect.ValueOf(tc)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return "", false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return "", false
	}
	f := v.FieldByName(field)
	if !f.IsValid() || f.Kind() != reflect.String || !f.CanInterface() {
		return "", false
	}
	return f.String(), true
}
