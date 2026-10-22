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

package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type guardHoldCase struct {
	name             string
	guard            string
	state            WorkloadState
	intent           ScaleIntent
	checkTerminating bool
	wantHeld         bool
	wantReason       string
	wantWhy          string
}

func TestGuardHold(t *testing.T) {
	for _, tc := range guardHoldCases {
		t.Run(tc.name, func(t *testing.T) {
			setTerminatingGate(t, tc.checkTerminating)
			g, ok := guardByName(tc.guard)
			if !ok {
				t.Fatalf("unknown guard %q", tc.guard)
			}
			if g.Reason() != tc.wantReason {
				t.Fatalf("Reason() = %q, want %q", g.Reason(), tc.wantReason)
			}
			held, why := g.Hold(tc.state, tc.intent)
			if held != tc.wantHeld || why != tc.wantWhy {
				t.Fatalf("Hold() = (%v, %q), want (%v, %q)", held, why, tc.wantHeld, tc.wantWhy)
			}
		})
	}
}

func guardByName(name string) (Guard, bool) {
	switch name {
	case "rollout":
		return rolloutGuard{}, true
	case "settle":
		return settleGuard{}, true
	default:
		return nil, false
	}
}

type guardOrderCase struct {
	name        string
	state       WorkloadState
	intent      ScaleIntent
	wantReason  string
	wantMessage string
}

// TestGuardOrder evaluates the guards slice. The flag is forced off so a
// row does not depend on whichever case ran before it.
func TestGuardOrder(t *testing.T) {
	for _, tc := range guardOrderCases {
		t.Run(tc.name, func(t *testing.T) {
			setTerminatingGate(t, false)
			got := evaluateGuards(tc.state, tc.intent)
			if got.reason != tc.wantReason || got.message != tc.wantMessage {
				t.Fatalf("got {%q %q}, want {%q %q}", got.reason, got.message, tc.wantReason, tc.wantMessage)
			}
		})
	}
}

func setTerminatingGate(t *testing.T, on bool) {
	t.Helper()
	prev := checkTerminatingReplicas
	checkTerminatingReplicas = on
	t.Cleanup(func() { checkTerminatingReplicas = prev })
}

// workloadObjectFields is one target object as data. A nil field is omitted.
// An int, int32, or int64 is stored as int64, which is what NestedInt64 reads.
// Any other type, such as a string standing in for an invalid field, is stored
// as itself.
type workloadObjectFields struct {
	spec        any
	ready       any
	total       any
	updated     any
	terminating any
	generation  any
	observed    any
}

type workloadStateCase struct {
	name   string
	kind   string
	fields workloadObjectFields
	want   WorkloadState
}

func TestReadWorkloadState(t *testing.T) {
	for _, tc := range workloadStateCases {
		t.Run(tc.name, func(t *testing.T) {
			got := readWorkloadState(workloadObject(t, tc.fields), tc.kind)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func workloadObject(t *testing.T, f workloadObjectFields) *unstructured.Unstructured {
	t.Helper()
	obj := map[string]any{}
	set := func(v any, fields ...string) {
		t.Helper()
		stored, ok := nestedJSONValue(v)
		if !ok {
			return
		}
		if err := unstructured.SetNestedField(obj, stored, fields...); err != nil {
			t.Fatalf("set %v: %v", fields, err)
		}
	}
	set(f.spec, "spec", "replicas")
	set(f.ready, "status", "readyReplicas")
	set(f.total, "status", "replicas")
	set(f.updated, "status", "updatedReplicas")
	set(f.terminating, "status", "terminatingReplicas")
	set(f.generation, "metadata", "generation")
	set(f.observed, "status", "observedGeneration")
	return &unstructured.Unstructured{Object: obj}
}

// nestedJSONValue converts a case-table number to the int64 NestedInt64
// accepts. nil means the field is omitted. A non-number is returned unchanged
// so an invalid type can be written on purpose.
func nestedJSONValue(v any) (any, bool) {
	switch n := v.(type) {
	case nil:
		return nil, false
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	default:
		return v, true
	}
}

// targetGuardCase is one object-level guard result: the fields build a
// Deployment-shaped object, kind selects the parse.
type targetGuardCase struct {
	name        string
	gen         int64
	observed    int64
	updated     int64
	kind        string
	spec        int64
	ready       int64
	target      int32
	wantReason  string
	wantMessage string
}

// deploymentObject builds the target a targetGuardCase describes.
// status.replicas stays at 5; the cases vary generation, updatedReplicas,
// spec.replicas, and readyReplicas.
func deploymentObject(gen, observed, updated, spec, ready int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"generation": gen},
		"spec":     map[string]any{"replicas": spec},
		"status": map[string]any{
			"observedGeneration": observed,
			"updatedReplicas":    updated,
			"replicas":           int64(5),
			"readyReplicas":      ready,
		},
	}}
}

// TestGuardsOnTarget parses each row's object and evaluates the guards.
func TestGuardsOnTarget(t *testing.T) {
	for _, tc := range targetGuardCases {
		t.Run(tc.name, func(t *testing.T) {
			state := readWorkloadState(deploymentObject(tc.gen, tc.observed, tc.updated, tc.spec, tc.ready), tc.kind)
			if state.Spec != int32(tc.spec) || state.Ready != int32(tc.ready) {
				t.Fatalf("parsed spec/ready = %d/%d, want %d/%d", state.Spec, state.Ready, tc.spec, tc.ready)
			}
			if state.Total != 5 || state.Updated != int32(tc.updated) || state.Generation != tc.gen || state.ObservedGeneration != tc.observed {
				t.Fatalf("parsed rollout fields = total %d updated %d gen %d observed %d", state.Total, state.Updated, state.Generation, state.ObservedGeneration)
			}
			got := evaluateGuards(state, ScaleIntent{From: state.Spec, To: tc.target})
			if got.reason != tc.wantReason || got.message != tc.wantMessage {
				t.Fatalf("got {%q %q}, want {%q %q}", got.reason, got.message, tc.wantReason, tc.wantMessage)
			}
		})
	}
}
