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

// Package ginkgocases builds Ginkgo table entries from case files.
//
// It lives apart from package cases so a test binary that only calls
// cases.Run or cases.Load does not import Ginkgo and does not register
// ginkgo flags.
package ginkgocases

import (
	"github.com/onsi/ginkgo/v2"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// Entries loads dir as Ginkgo table entries, one Entry per file.
// description supplies the entry text; a nil description, or a result of "",
// uses the file stem. A load error panics so suite setup fails loudly.
func Entries[T any](dir string, description func(T) string) []ginkgo.TableEntry {
	files, err := cases.LoadFiles[T](dir)
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
