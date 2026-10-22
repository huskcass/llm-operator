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
	"math"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// stateInput is the WorkloadState a guard case feeds Hold or evaluateGuards.
// Zero fields are left at the Go zero, which is what an omitted YAML key means.
type stateInput struct {
	Spec                int32 `json:"spec"`
	Total               int32 `json:"total"`
	Updated             int32 `json:"updated"`
	Ready               int32 `json:"ready"`
	Terminating         int32 `json:"terminating"`
	TerminatingReported bool  `json:"terminatingReported"`
	Generation          int64 `json:"generation"`
	ObservedGeneration  int64 `json:"observedGeneration"`
	TracksRollout       bool  `json:"tracksRollout"`
}

func (s stateInput) state() WorkloadState { return WorkloadState(s) }

type intentInput struct {
	From int32 `json:"from"`
	To   int32 `json:"to"`
}

func (in intentInput) intent() ScaleIntent { return ScaleIntent(in) }

type guardHoldFile struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		Guard            string      `json:"guard"`
		State            stateInput  `json:"state"`
		Intent           intentInput `json:"intent"`
		CheckTerminating bool        `json:"checkTerminating"`
	} `json:"inputs"`
	Expected struct {
		Held   bool   `json:"held"`
		Reason string `json:"reason"`
		Why    string `json:"why"`
	} `json:"expected"`
}

func TestGuardHold(t *testing.T) {
	cases.Run(t, "testdata/guards/hold", func(t *testing.T, tc guardHoldFile) {
		setTerminatingGate(t, tc.Inputs.CheckTerminating)
		g, ok := guardByName(tc.Inputs.Guard)
		if !ok {
			t.Fatalf("unknown guard %q", tc.Inputs.Guard)
		}
		if g.Reason() != tc.Expected.Reason {
			t.Fatalf("Reason() = %q, want %q", g.Reason(), tc.Expected.Reason)
		}
		held, why := g.Hold(tc.Inputs.State.state(), tc.Inputs.Intent.intent())
		if held != tc.Expected.Held || why != tc.Expected.Why {
			t.Fatalf("Hold() = (%v, %q), want (%v, %q)", held, why, tc.Expected.Held, tc.Expected.Why)
		}
	})
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

type guardOrderFile struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		State  stateInput  `json:"state"`
		Intent intentInput `json:"intent"`
	} `json:"inputs"`
	Expected struct {
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"expected"`
}

// TestGuardOrder evaluates the guards slice. The flag is forced off so a
// row does not depend on whichever case ran before it.
func TestGuardOrder(t *testing.T) {
	cases.Run(t, "testdata/guards/order", func(t *testing.T, tc guardOrderFile) {
		setTerminatingGate(t, false)
		got := evaluateGuards(tc.Inputs.State.state(), tc.Inputs.Intent.intent())
		if got.reason != tc.Expected.Reason || got.message != tc.Expected.Message {
			t.Fatalf("got {%q %q}, want {%q %q}", got.reason, got.message, tc.Expected.Reason, tc.Expected.Message)
		}
	})
}

func setTerminatingGate(t *testing.T, on bool) {
	t.Helper()
	prev := checkTerminatingReplicas
	checkTerminatingReplicas = on
	t.Cleanup(func() { checkTerminatingReplicas = prev })
}

type workloadStateFile struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		Kind   string                     `json:"kind"`
		Object *unstructured.Unstructured `json:"object"`
	} `json:"inputs"`
	Expected stateInput `json:"expected"`
}

func TestReadWorkloadState(t *testing.T) {
	cases.Run(t, "testdata/guards/workload-state", func(t *testing.T, tc workloadStateFile) {
		normalizeUnstructured(tc.Inputs.Object)
		got := readWorkloadState(tc.Inputs.Object, tc.Inputs.Kind)
		if got != tc.Expected.state() {
			t.Fatalf("got %+v, want %+v", got, tc.Expected.state())
		}
	})
}

// targetGuardFile is one object-level guard result. The object is a
// Deployment-shaped document; kind selects the parse. status.replicas stays
// at 5 in every file. readyReplicas 0 is the raw count and is not settled.
type targetGuardFile struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Inputs      struct {
		Kind   string                     `json:"kind"`
		Object *unstructured.Unstructured `json:"object"`
		Target int32                      `json:"target"`
	} `json:"inputs"`
	Expected struct {
		Spec               int32  `json:"spec"`
		Ready              int32  `json:"ready"`
		Total              int32  `json:"total"`
		Updated            int32  `json:"updated"`
		Generation         int64  `json:"generation"`
		ObservedGeneration int64  `json:"observedGeneration"`
		Reason             string `json:"reason"`
		Message            string `json:"message"`
	} `json:"expected"`
}

// TestGuardsOnTarget parses each file's object and evaluates the guards.
func TestGuardsOnTarget(t *testing.T) {
	cases.Run(t, "testdata/guards/target", func(t *testing.T, tc targetGuardFile) {
		normalizeUnstructured(tc.Inputs.Object)
		state := readWorkloadState(tc.Inputs.Object, tc.Inputs.Kind)
		exp := tc.Expected
		if state.Spec != exp.Spec || state.Ready != exp.Ready {
			t.Fatalf("parsed spec/ready = %d/%d, want %d/%d", state.Spec, state.Ready, exp.Spec, exp.Ready)
		}
		if state.Total != exp.Total || state.Updated != exp.Updated ||
			state.Generation != exp.Generation || state.ObservedGeneration != exp.ObservedGeneration {
			t.Fatalf("parsed rollout fields = total %d updated %d gen %d observed %d",
				state.Total, state.Updated, state.Generation, state.ObservedGeneration)
		}
		got := evaluateGuards(state, ScaleIntent{From: state.Spec, To: tc.Inputs.Target})
		if got.reason != exp.Reason || got.message != exp.Message {
			t.Fatalf("got {%q %q}, want {%q %q}", got.reason, got.message, exp.Reason, exp.Message)
		}
	})
}

// normalizeUnstructured turns whole-number float64 values into int64.
// NestedInt64 only accepts int64. A string standing in for an invalid field
// is left alone so it still reads as missing.
func normalizeUnstructured(obj *unstructured.Unstructured) {
	if obj == nil || obj.Object == nil {
		return
	}
	obj.Object = normalizeJSON(obj.Object).(map[string]any)
}

func normalizeJSON(v any) any {
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n == math.Trunc(n) &&
			n >= math.MinInt64 && n <= math.MaxInt64 {
			return int64(n)
		}
		return n
	case map[string]any:
		for k, child := range n {
			n[k] = normalizeJSON(child)
		}
		return n
	case []any:
		for i := range n {
			n[i] = normalizeJSON(n[i])
		}
		return n
	default:
		return v
	}
}
