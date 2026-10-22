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
// Files are discovered recursively under a directory. A name ending in
// ".yaml" is a case. A name ending in ".yml" is an error, so a second
// extension cannot hide a case or make two files share a stem. Any other
// name (a README, a note) is ignored. The suffix check is case-sensitive,
// matching the ".yaml" files already in the tree. The result is ordered by
// the slash-separated path relative to that directory, so a rename or a new
// file changes order only by that path.
//
// Each file must contain exactly one mapping. An empty file, a comment-only
// file, a null document (`null`, `~`, or a bare `---`), or a second document
// fails the load. A trailing `---` is a second, empty document and fails too.
// The mapping is decoded with sigs.k8s.io/yaml in strict mode: an unknown
// field, a duplicate key, or a value of the wrong type fails the load.
// Mapping keys are case-sensitive. A key must equal the json name of a field
// (or the field name when the struct has no json name). An anonymous embedded
// struct contributes its fields, as encoding/json does. A type with
// UnmarshalJSON, including Duration and Kubernetes types that decode
// themselves, is a leaf; other structs, slices, maps, and pointers are
// walked. An empty directory, a missing directory, and an unreadable file
// fail as well.
//
// A decoded case may carry Name and Description. Name, when set, must equal the
// file stem (the base name without ".yaml"). Description is the sentence
// runners use as the subtest text; when it is empty, the stem is used
// instead. Ginkgo table entries are built by package ginkgocases, so this
// package does not import Ginkgo.
//
// Durations in a case are Go duration strings ("30s", "1h", "-2h", "0s")
// decoded into Duration. Only a pointer to Duration may be omitted or null.
// A non-pointer Duration rejects null and treats an omission as zero. A
// pointer to zero is the string "0s", not a bare 0.
//
// Keys are decoded with YAML 1.1 rules. An unquoted n, y, yes, no, on, or off
// is a boolean, not a string, and strict decoding then reports it as an
// unknown field. Quote any such key.
package cases

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	yamlv3 "gopkg.in/yaml.v3"
	"sigs.k8s.io/yaml"
)

// Duration is a time.Duration stored in YAML as a Go duration string.
type Duration time.Duration

// Std returns the duration as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// UnmarshalJSON accepts a JSON string in Go duration syntax.
// Null is rejected: only a pointer to Duration can represent an omitted value.
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
		tc, err := decodeCase[T](rel, body)
		if err != nil {
			return nil, err
		}
		stem := strings.TrimSuffix(filepath.Base(rel), ".yaml")
		if err := checkName(tc, stem, rel); err != nil {
			return nil, err
		}
		out = append(out, File[T]{Rel: rel, Stem: stem, Case: tc})
	}
	return out, nil
}

// MustLoad is LoadFiles for a test container that has no testing.T yet.
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

// SpecName is the subtest text for a case: its Description, or stem.
func SpecName[T any](tc T, stem string) string {
	if d, ok := stringField(tc, "Description"); ok && d != "" {
		return d
	}
	return stem
}

func decodeCase[T any](rel string, body []byte) (T, error) {
	var zero T
	if err := oneMapping(body); err != nil {
		return zero, fmt.Errorf("cases: decode %s: %w", rel, err)
	}
	var tc T
	if err := yaml.UnmarshalStrict(body, &tc); err != nil {
		return zero, fmt.Errorf("cases: decode %s: %w", rel, err)
	}
	var tree any
	if err := yaml.Unmarshal(body, &tree); err != nil {
		return zero, fmt.Errorf("cases: decode %s: %w", rel, err)
	}
	if err := checkKeys(tree, reflect.TypeOf(tc)); err != nil {
		return zero, fmt.Errorf("cases: decode %s: %w", rel, err)
	}
	return tc, nil
}

// oneMapping requires exactly one YAML document, and that document to be a
// non-null mapping. yaml.v3 counts documents; sigs.k8s.io/yaml then decodes
// the bytes, so YAML 1.1 values are unchanged.
func oneMapping(body []byte) error {
	dec := yamlv3.NewDecoder(bytes.NewReader(body))
	var (
		count int
		doc   yamlv3.Node
	)
	for {
		var next yamlv3.Node
		err := dec.Decode(&next)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		doc = next
	}
	if count == 0 {
		return fmt.Errorf("no document")
	}
	if count != 1 {
		return fmt.Errorf("%d documents, want 1", count)
	}
	if doc.Kind != yamlv3.DocumentNode || len(doc.Content) != 1 {
		return fmt.Errorf("document is empty")
	}
	n := doc.Content[0]
	if n.Tag == "!!null" {
		return fmt.Errorf("document is null")
	}
	if n.Kind != yamlv3.MappingNode {
		return fmt.Errorf("document must be a mapping")
	}
	return nil
}

func yamlRels(dir string) ([]string, error) {
	var rels []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// .yml is an error rather than a skip: one extension keeps the stem
		// unambiguous, and a mis-named file must not vanish from the run.
		if strings.HasSuffix(d.Name(), ".yml") {
			return fmt.Errorf("%s: .yml files are not loaded; rename it to .yaml", rel)
		}
		if !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		rels = append(rels, rel)
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
