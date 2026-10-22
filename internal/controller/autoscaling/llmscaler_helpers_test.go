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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	autoscalingv1alpha1 "github.com/modelsphere/llm-operator/api/autoscaling/v1alpha1"
)

// pastDeferBase is the deferredSince instant for TestPastDeferTimeout.
// Whole seconds, so nothing about the deadline depends on the clock reading.
var pastDeferBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// forcedWriteBase is the "last forced write" instant for TestForcedWritePace.
var forcedWriteBase = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// stabilizationBase is the origin for TestStabilizeDesired. A fixed base, not
// time.Now(), so a slow run cannot slide a window boundary.
var stabilizationBase = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

var stabilizationKey = types.NamespacedName{Namespace: "ns", Name: "s"}

// deferralRequeueSync is the sync period passed to every deferralRequeueAfter row.
const deferralRequeueSync = 15 * time.Second

type deferTimeoutCase struct {
	name         string
	deferTimeout *int32
	want         time.Duration
}

func TestDeferTimeout(t *testing.T) {
	for _, tc := range deferTimeoutCases {
		t.Run(tc.name, func(t *testing.T) {
			scaler := &autoscalingv1alpha1.LLMScaler{}
			scaler.Spec.DeferTimeoutSeconds = tc.deferTimeout
			got := deferTimeout(scaler)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

type pastDeferTimeoutCase struct {
	name     string
	nilSince bool
	timeout  time.Duration
	elapsed  time.Duration
	want     bool
}

func TestPastDeferTimeout(t *testing.T) {
	for _, tc := range pastDeferTimeoutCases {
		t.Run(tc.name, func(t *testing.T) {
			var since *metav1.Time
			if !tc.nilSince {
				stamped := metav1.NewTime(pastDeferBase)
				since = &stamped
			}
			got := pastDeferTimeout(since, tc.timeout, pastDeferBase.Add(tc.elapsed))
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

type deferralRequeueAfterCase struct {
	name    string
	holding bool
	timeout time.Duration
	until   time.Duration
	pacing  time.Duration
	want    time.Duration
}

func TestDeferralRequeueAfter(t *testing.T) {
	for _, tc := range deferralRequeueAfterCases {
		t.Run(tc.name, func(t *testing.T) {
			got := deferralRequeueAfter(deferralRequeueSync, tc.holding, tc.timeout, tc.until, tc.pacing)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

type forcedWritePaceCase struct {
	name          string
	recorded      bool
	sync          time.Duration
	at            time.Duration
	wantAllowed   bool
	wantRemaining *time.Duration
}

func TestForcedWritePace(t *testing.T) {
	for _, tc := range forcedWritePaceCases {
		t.Run(tc.name, func(t *testing.T) {
			allowed, remaining := forcedWritePace(forcedWriteBase, tc.recorded, tc.sync, forcedWriteBase.Add(tc.at))
			if allowed != tc.wantAllowed {
				t.Fatalf("allowed=%v, want %v", allowed, tc.wantAllowed)
			}
			if tc.wantRemaining != nil && remaining != *tc.wantRemaining {
				t.Fatalf("remaining=%s, want %s", remaining, *tc.wantRemaining)
			}
		})
	}
}

// TestDeferralMemoryClearedOnEndAndDelete checks the two ways the in-memory
// pace record goes away: the deferral ending, and the scaler being deleted.
func TestDeferralMemoryClearedOnEndAndDelete(t *testing.T) {
	r := &LLMScalerReconciler{}
	key := types.NamespacedName{Namespace: "ns", Name: "s"}
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	r.recordForcedWrite(key, now)
	scaler := &autoscalingv1alpha1.LLMScaler{}
	stamped := metav1.NewTime(now)
	scaler.Status.DeferredSince = &stamped

	r.endDeferral(key, scaler)
	if scaler.Status.DeferredSince != nil {
		t.Fatal("endDeferral left deferredSince set")
	}
	if _, ok := r.lastForcedWriteAt(key); ok {
		t.Fatal("endDeferral left the forced-write record")
	}

	r.recordForcedWrite(key, now)
	r.forgetRecommendations(key)
	if _, ok := r.lastForcedWriteAt(key); ok {
		t.Fatal("forgetRecommendations left the forced-write record")
	}
}

// TestReasonDeferralTimedOut pins the published condition reason, which users
// match on in alerts and kubectl output.
func TestReasonDeferralTimedOut(t *testing.T) {
	if autoscalingv1alpha1.ReasonDeferralTimedOut != "DeferralTimedOut" {
		t.Fatalf("ReasonDeferralTimedOut = %q, want DeferralTimedOut", autoscalingv1alpha1.ReasonDeferralTimedOut)
	}
}

// deploymentObject builds a Deployment-shaped object for the guard tests.
// status.replicas stays at 5; the cases vary generation and updatedReplicas.
func deploymentObject(gen, observed, updated int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"generation": gen},
		"status": map[string]any{
			"observedGeneration": observed,
			"updatedReplicas":    updated,
			"replicas":           int64(5),
		},
	}}
}

type scaleDeferReasonCase struct {
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

func TestScaleDeferReason(t *testing.T) {
	for _, tc := range scaleDeferReasonCases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaleDeferReason(deploymentObject(tc.gen, tc.observed, tc.updated), tc.kind, tc.spec, tc.ready, tc.target)
			if got.reason != tc.wantReason || got.message != tc.wantMessage {
				t.Fatalf("got {%q %q}, want {%q %q}", got.reason, got.message, tc.wantReason, tc.wantMessage)
			}
		})
	}
}

type stabilizationOp struct {
	at        time.Duration
	recommend *int32
	holdAt    *int32
	want      *int32
}

type stabilizationCase struct {
	name   string
	window time.Duration
	ops    []stabilizationOp
}

// TestStabilizeDesired runs each stabilization sequence on its own reconciler.
// TestHoldAfterScaleDown's steps, including the window-0 reconciler, are rows
// of the same table.
func TestStabilizeDesired(t *testing.T) {
	for _, tc := range stabilizationSequenceCases {
		t.Run(tc.name, func(t *testing.T) {
			r := &LLMScalerReconciler{}
			for _, op := range tc.ops {
				now := stabilizationBase.Add(op.at)
				switch {
				case op.holdAt != nil && op.recommend != nil:
					t.Fatalf("op at %s sets both holdAt and recommend", op.at)
				case op.holdAt != nil:
					r.holdAfterScaleDown(stabilizationKey, *op.holdAt, tc.window, now)
				case op.recommend != nil:
					if op.want == nil {
						t.Fatalf("recommend at %s has no want", op.at)
					}
					got := r.stabilizeDesired(stabilizationKey, *op.recommend, tc.window, now)
					if got != *op.want {
						t.Fatalf("at %s: got %d, want %d", op.at, got, *op.want)
					}
				default:
					t.Fatalf("op at %s has neither holdAt nor recommend", op.at)
				}
			}
		})
	}
}

type parseTargetValueCase struct {
	name    string
	in      string
	want    float64
	wantErr bool
}

func TestParseTargetValue(t *testing.T) {
	for _, tc := range parseTargetValueCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseTargetValue(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseTargetValue(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("parseTargetValue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

type queryPrometheusScalarCase struct {
	name            string
	result          string
	want            float64
	wantErrContains []string
}

func TestQueryPrometheusScalar(t *testing.T) {
	for _, tc := range queryPrometheusScalarCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"status":"success","data":{"result":%s}}`, tc.result)
			}))
			defer srv.Close()

			got, err := queryPrometheusScalar(srv.URL, "some_query", nil)
			if len(tc.wantErrContains) == 0 {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got value %v", tc.wantErrContains, got)
			}
			for _, want := range tc.wantErrContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

type computeDesiredCase struct {
	name        string
	sample      string
	ready       int64
	minReplicas int32
	maxReplicas int32
	metricName  string
	query       string
	target      string
	wantHave    bool
	wantDesired int32
}

// TestComputeDesiredFromMetrics covers non-finite samples and scale-from-zero.
func TestComputeDesiredFromMetrics(t *testing.T) {
	for _, tc := range computeDesiredFromMetricsCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w,
					`{"status":"success","data":{"result":[{"metric":{},"value":[0,"%s"]}]}}`, tc.sample)
			}))
			defer srv.Close()

			scaler := &autoscalingv1alpha1.LLMScaler{
				Spec: autoscalingv1alpha1.LLMScalerSpec{
					ServerAddress: srv.URL,
					MinReplicas:   tc.minReplicas,
					MaxReplicas:   tc.maxReplicas,
					Metrics: []autoscalingv1alpha1.MetricSpec{{
						Name:   tc.metricName,
						Query:  tc.query,
						Target: tc.target,
					}},
				},
			}
			desired, haveMetric := (&LLMScalerReconciler{}).computeDesiredFromMetrics(context.Background(), scaler, tc.ready)
			if haveMetric != tc.wantHave {
				t.Errorf("haveMetric = %v, want %v", haveMetric, tc.wantHave)
			}
			if desired != tc.wantDesired {
				t.Errorf("desired = %d, want %d", desired, tc.wantDesired)
			}
		})
	}
}

type scaleGuardCase struct {
	name    string
	current int32
	desired int32
	maxStep int32
	want    int32
}

func TestScaleGuard(t *testing.T) {
	for _, tc := range scaleGuardCases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaleGuard(tc.current, tc.desired, tc.maxStep)
			if got != tc.want {
				t.Fatalf("scaleGuard(%d, %d, %d) = %d, want %d", tc.current, tc.desired, tc.maxStep, got, tc.want)
			}
		})
	}
}
