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

package ginkgocases

import (
	"testing"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// sample matches the documents under ../testdata/ok.
type sample struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		Value int             `json:"value"`
		Wait  cases.Duration  `json:"wait,omitempty"`
		Maybe *cases.Duration `json:"maybe,omitempty"`
		Empty *cases.Duration `json:"empty,omitempty"`
	} `json:"inputs"`
	Expected struct {
		Value int `json:"value"`
	} `json:"expected"`
}

func TestEntriesFollowsDescription(t *testing.T) {
	entries := Entries("../testdata/ok", func(s sample) string { return s.Description })
	if len(entries) != 2 {
		t.Fatalf("got %d entries", len(entries))
	}
	entries = Entries[sample]("../testdata/ok", nil)
	if len(entries) != 2 {
		t.Fatalf("nil description: got %d entries", len(entries))
	}
}
