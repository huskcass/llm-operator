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
	"time"

	autoscalingv1alpha1 "github.com/modelsphere/llm-operator/api/autoscaling/v1alpha1"
)

// deferTimeoutCases cover a nil spec (CRD default / in-memory object) and an
// explicit 0, which must stay distinguishable so "never force" is opt-in.
var deferTimeoutCases = []deferTimeoutCase{
	{name: "nil spec", want: time.Duration(autoscalingv1alpha1.DefaultDeferTimeoutSeconds) * time.Second},
	{name: "explicit 0", deferTimeout: ptr(int32(0)), want: 0},
	{name: "explicit 30", deferTimeout: ptr(int32(30)), want: 30 * time.Second},
}

// pastDeferTimeoutCases pin the deadline: equal to the timeout forces, short of
// it does not, and 0 never does. Elapsed is added to the runner's fixed base.
var pastDeferTimeoutCases = []pastDeferTimeoutCase{
	{name: "nil deferredSince", nilSince: true, timeout: time.Minute, elapsed: time.Hour, want: false},
	{name: "timeout 0", timeout: 0, elapsed: 24 * time.Hour, want: false},
	{name: "59s of a 60s timeout", timeout: time.Minute, elapsed: 59 * time.Second, want: false},
	{name: "exactly the timeout", timeout: time.Minute, elapsed: time.Minute, want: true},
	{name: "past the timeout", timeout: time.Minute, elapsed: time.Minute + time.Second, want: true},
}

// deferralRequeueAfterCases is how long a deferred sync waits. The runner passes
// a 15s sync period, the controller default; rows whose want is 15s are the ones
// that stay on that period.
var deferralRequeueAfterCases = []deferralRequeueAfterCase{
	{name: "deadline sooner than sync period", holding: true, timeout: time.Minute, until: 5 * time.Second, want: 5 * time.Second},
	{name: "deadline later than sync period", holding: true, timeout: time.Minute, until: 30 * time.Second, want: 15 * time.Second},
	{name: "deadline already passed", holding: true, timeout: time.Minute, until: -time.Second, want: time.Second},
	{name: "deadline due this instant", holding: true, timeout: time.Minute, until: 0, want: time.Second},
	{name: "deadline under the 1s floor", holding: true, timeout: time.Minute, until: 200 * time.Millisecond, want: time.Second},
	{name: "timeout 0 stays on sync period", holding: true, timeout: 0, until: 5 * time.Second, want: 15 * time.Second},
	{name: "not holding stays on sync period", holding: false, timeout: time.Minute, until: 5 * time.Second, want: 15 * time.Second},
	{name: "pacing sooner than sync period", holding: true, timeout: time.Minute, until: -time.Second, pacing: 4 * time.Second, want: 4 * time.Second},
	{name: "pacing later than sync period", holding: true, timeout: time.Minute, until: -time.Second, pacing: 30 * time.Second, want: 15 * time.Second},
	{name: "pacing under the 1s floor", holding: true, timeout: time.Minute, until: -time.Second, pacing: 100 * time.Millisecond, want: time.Second},
}

// forcedWritePaceCases cover the per-process gap between forced writes.
// wantRemaining nil leaves the remaining duration unchecked.
var forcedWritePaceCases = []forcedWritePaceCase{
	{name: "no record", recorded: false, sync: 15 * time.Second, at: 0, wantAllowed: true, wantRemaining: ptr(time.Duration(0))},
	{name: "zero sync period", recorded: true, sync: 0, at: time.Hour, wantAllowed: true, wantRemaining: ptr(time.Duration(0))},
	{name: "exactly one sync period", recorded: true, sync: 15 * time.Second, at: 15 * time.Second, wantAllowed: true},
	{name: "just recorded", recorded: true, sync: 15 * time.Second, at: 0, wantAllowed: false},
	{name: "14s into a 15s period", recorded: true, sync: 15 * time.Second, at: 14 * time.Second, wantAllowed: false, wantRemaining: ptr(time.Second)},
}

// stabilizationSequenceCases run an ordered list of recommend/hold ops on one
// fresh reconciler per case. at is an offset from the runner's fixed base,
// not the wall clock. TestStabilizeDesired and TestHoldAfterScaleDown are both
// rows here, plus the window-0 reconciler.
var stabilizationSequenceCases = []stabilizationCase{
	{
		// First recommendation sticks, a dip inside the window holds the peak,
		// the peak expires after the window, and a scale-up is immediate.
		name:   "peak holds inside the window then expires",
		window: 60 * time.Second,
		ops: []stabilizationOp{
			{at: 0, recommend: ptr(int32(5)), want: ptr(int32(5))},
			// Metric dipped to 1, but the peak of 5 is still within the window.
			{at: 10 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(5))},
			// After the window the peak has expired.
			{at: 61 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(1))},
			// Scale-up is immediate regardless of history.
			{at: 61 * time.Second, recommend: ptr(int32(9)), want: ptr(int32(9))},
		},
	},
	{
		// Pinning the written count after a scale-down paces the next one a
		// full window later, rather than letting the descent resolve off one
		// collapsed reading.
		name:   "pin after a scale-down paces the next step",
		window: 300 * time.Second,
		ops: []stabilizationOp{
			// Fleet of 8, metric collapses; the peak is still in the window.
			{at: 0, recommend: ptr(int32(8)), want: ptr(int32(8))},
			// A window later the peak has expired and the low recommendation
			// wins. The controller then writes 6 (an 8 -> 1 recommendation
			// capped to a 2-replica step) and pins it.
			{at: 301 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(1))},
			{at: 301 * time.Second, holdAt: ptr(int32(6))},
			// Within the window the fleet is held at 6 even though the metric
			// still reads idle. Before the pin these would each have shrunk it.
			{at: 311 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(6))},
			{at: 400 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(6))},
			{at: 600 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(6))},
			// Once the pin ages out, the next step is allowed.
			{at: 602 * time.Second, recommend: ptr(int32(1)), want: ptr(int32(1))},
			// Scale-up is never blocked by the pin.
			{at: 700 * time.Second, holdAt: ptr(int32(6))},
			{at: 710 * time.Second, recommend: ptr(int32(9)), want: ptr(int32(9))},
		},
	},
	{
		// With stabilization off there is nothing to pace with, so the pin is a no-op.
		name:   "window 0 makes the pin a no-op",
		window: 0,
		ops: []stabilizationOp{
			{at: 0, holdAt: ptr(int32(6))},
			{at: 0, recommend: ptr(int32(1)), want: ptr(int32(1))},
		},
	},
}

// parseTargetValueCases cover the target parser. It is the denominator of the
// scaling ratio and must never yield zero, a negative, or a non-finite value.
var parseTargetValueCases = []parseTargetValueCase{
	{name: "plain integer", in: "5", want: 5},
	{name: "ratio", in: "0.8", want: 0.8},
	{name: "surrounding whitespace", in: "  2.5 ", want: 2.5},
	{name: "percent suffix is rejected, not stripped", in: "80%", wantErr: true},
	{name: "zero would make the ratio infinite", in: "0", wantErr: true},
	{name: "negative would invert scaling", in: "-1", wantErr: true},
	{name: "NaN parses as a float but is not usable", in: "NaN", wantErr: true},
	{name: "Inf parses as a float but is not usable", in: "+Inf", wantErr: true},
	{name: "not a number", in: "abc", wantErr: true},
	{name: "empty", in: "", wantErr: true},
}

// queryPrometheusScalarCases resolve an instant-query vector: exactly one series
// is a value, and anything else is an error the caller skips the metric on.
var queryPrometheusScalarCases = []queryPrometheusScalarCase{
	{
		name:   "single series resolves",
		result: `[{"metric":{},"value":[0,"1.5"]}]`,
		want:   1.5,
	},
	{
		name:            "empty vector is not zero",
		result:          `[]`,
		wantErrContains: []string{"returned no data"},
	},
	{
		name: "an under-aggregated query is rejected, naming the series",
		result: `[{"metric":{"__name__":"m","model":"qwen","backend":"10.0.0.1:8000"},"value":[0,"1"]},
		          {"metric":{"__name__":"m","model":"qwen","backend":"10.0.0.2:8000"},"value":[0,"9"]}]`,
		wantErrContains: []string{"returned 2 series"},
	},
	{
		// The multi-series error has to point at the label to aggregate away,
		// so it quotes both series with their labels sorted.
		name: "multi-series error names both label sets",
		result: `[
			{"metric":{"model":"qwen","backend":"10.0.0.1:8000"},"value":[0,"1"]},
			{"metric":{"model":"qwen","backend":"10.0.0.2:8000"},"value":[0,"9"]}]`,
		wantErrContains: []string{
			`{backend="10.0.0.1:8000",model="qwen"}`,
			`{backend="10.0.0.2:8000",model="qwen"}`,
			"avg(...)",
		},
	},
}

// computeDesiredFromMetricsCases cover a non-finite sample (not a measurement)
// and the scale-from-zero baseline. A NaN or Inf sample must not set haveMetric:
// histogram_quantile over an empty window returns NaN, and treating that as zero
// would read as "scale to minReplicas". With no ready replicas a positive signal
// is evaluated against a single-replica baseline, so the fleet wakes; a zero
// signal still recommends 0.
var computeDesiredFromMetricsCases = []computeDesiredCase{
	{name: "NaN", sample: "NaN", ready: 4, metricName: "guardrail", query: "histogram_quantile(0.95, x)", target: "2"},
	{name: "+Inf", sample: "+Inf", ready: 4, metricName: "guardrail", query: "histogram_quantile(0.95, x)", target: "2"},
	{name: "-Inf", sample: "-Inf", ready: 4, metricName: "guardrail", query: "histogram_quantile(0.95, x)", target: "2"},
	// ceil(1 * 0.85/0.5) = 2, not ceil(0 * 0.85/0.5) = 0.
	{name: "positive signal wakes the fleet", sample: mockMetricValue, ready: 0, minReplicas: 0, maxReplicas: 5, metricName: "queue", query: "sum(queue_depth)", target: "0.5", wantHave: true, wantDesired: 2},
	// A zero signal still recommends 0.
	{name: "zero signal recommends zero", sample: "0", ready: 0, minReplicas: 0, maxReplicas: 5, metricName: "queue", query: "sum(queue_depth)", target: "0.5", wantHave: true, wantDesired: 0},
}

// scaleGuardCases are the scale-down rate limiter. Scale-up and a no-op pass through.
var scaleGuardCases = []scaleGuardCase{
	{name: "unlimited when maxStep is 0", current: 10, desired: 1, maxStep: 0, want: 1},
	{name: "caps a burst scale-down", current: 10, desired: 1, maxStep: 2, want: 8},
	{name: "passes through a step within the cap", current: 10, desired: 9, maxStep: 2, want: 9},
	{name: "passes through a step exactly at the cap", current: 10, desired: 8, maxStep: 2, want: 8},
	{name: "leaves scale-up untouched", current: 2, desired: 10, maxStep: 1, want: 10},
	{name: "leaves a no-op untouched", current: 5, desired: 5, maxStep: 1, want: 5},
}
