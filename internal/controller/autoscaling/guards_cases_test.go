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
	autoscalingv1alpha1 "github.com/modelsphere/llm-operator/api/autoscaling/v1alpha1"
)

// guardHoldCases run one guard over a WorkloadState and a ScaleIntent.
// checkTerminating is written onto the package flag for the row and restored.
// Rollout rows cover each in-progress signal, a finished rollout, a
// non-Deployment, and a hold in each direction. Settle rows cover direction,
// a ready count that has not caught up (including 0), and the terminating
// gate on and off.
var guardHoldCases = []guardHoldCase{
	{
		name:  "rollout: generation not observed holds a scale-up",
		guard: "rollout",
		state: WorkloadState{
			Spec: 5, Total: 5, Updated: 5, Ready: 5,
			Generation: 4, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:     ScaleIntent{From: 5, To: 7},
		wantHeld:   true,
		wantReason: autoscalingv1alpha1.ReasonRolloutInProgress,
		wantWhy:    msgRolloutInProgress,
	},
	{
		name:  "rollout: updated replicas behind spec holds a scale-down",
		guard: "rollout",
		state: WorkloadState{
			Spec: 5, Total: 4, Updated: 4, Ready: 4,
			Generation: 3, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:     ScaleIntent{From: 5, To: 3},
		wantHeld:   true,
		wantReason: autoscalingv1alpha1.ReasonRolloutInProgress,
		wantWhy:    msgRolloutInProgress,
	},
	{
		name:  "rollout: old-template pods hold a no-op",
		guard: "rollout",
		state: WorkloadState{
			Spec: 5, Total: 6, Updated: 5, Ready: 5,
			Generation: 3, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:     ScaleIntent{From: 5, To: 5},
		wantHeld:   true,
		wantReason: autoscalingv1alpha1.ReasonRolloutInProgress,
		wantWhy:    msgRolloutInProgress,
	},
	{
		name:  "rollout: fully rolled out does not hold",
		guard: "rollout",
		state: WorkloadState{
			Spec: 5, Total: 5, Updated: 5, Ready: 5,
			Generation: 3, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:     ScaleIntent{From: 5, To: 4},
		wantReason: autoscalingv1alpha1.ReasonRolloutInProgress,
	},
	{
		name:  "rollout: non-Deployment is never held",
		guard: "rollout",
		state: WorkloadState{
			Spec: 5, Total: 6, Updated: 0, Ready: 5,
			Generation: 4, ObservedGeneration: 1,
		},
		intent:     ScaleIntent{From: 5, To: 7},
		wantReason: autoscalingv1alpha1.ReasonRolloutInProgress,
	},
	{
		name:             "settle: scale-up is not held",
		guard:            "settle",
		state:            WorkloadState{Spec: 5, Ready: 2},
		intent:           ScaleIntent{From: 5, To: 7},
		checkTerminating: false,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
	{
		name:             "settle: no-op is not held",
		guard:            "settle",
		state:            WorkloadState{Spec: 5, Ready: 2},
		intent:           ScaleIntent{From: 5, To: 5},
		checkTerminating: false,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
	{
		name:             "settle: ready differs from spec",
		guard:            "settle",
		state:            WorkloadState{Spec: 5, Ready: 3},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: false,
		wantHeld:         true,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
		wantWhy:          msgScaleDownNotSettled,
	},
	{
		// A ready count of 0 is the raw missing-field value. Treating it as
		// spec would skip the guard.
		name:             "settle: ready count of 0 is not settled",
		guard:            "settle",
		state:            WorkloadState{Spec: 5, Ready: 0},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: false,
		wantHeld:         true,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
		wantWhy:          msgScaleDownNotSettled,
	},
	{
		name:             "settle: ready back at spec",
		guard:            "settle",
		state:            WorkloadState{Spec: 5, Ready: 5},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: false,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
	{
		name:  "settle: terminating pods with the gate off",
		guard: "settle",
		state: WorkloadState{
			Spec: 5, Ready: 5, Terminating: 2, TerminatingReported: true,
		},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: false,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
	{
		name:  "settle: terminating pods with the gate on",
		guard: "settle",
		state: WorkloadState{
			Spec: 5, Ready: 5, Terminating: 2, TerminatingReported: true,
		},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: true,
		wantHeld:         true,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
		wantWhy:          msgScaleDownNotSettled,
	},
	{
		name:  "settle: terminating count of 0 with the gate on",
		guard: "settle",
		state: WorkloadState{
			Spec: 5, Ready: 5, Terminating: 0, TerminatingReported: true,
		},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: true,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
	{
		// The count is ignored when the field was not reported. A non-zero
		// Terminating here would still be settled; the parser stores 0 in
		// that case, so this row is what makes the flag observable.
		name:  "settle: terminating not reported with the gate on",
		guard: "settle",
		state: WorkloadState{
			Spec: 5, Ready: 5, Terminating: 4, TerminatingReported: false,
		},
		intent:           ScaleIntent{From: 5, To: 4},
		checkTerminating: true,
		wantReason:       autoscalingv1alpha1.ReasonScaleDownSettling,
	},
}

// guardOrderCases walk the guards slice. The first hold wins: a rollout is
// reported ahead of a scale-down that has not settled.
var guardOrderCases = []guardOrderCase{
	{
		name: "rollout wins over an unsettled scale-down",
		state: WorkloadState{
			Spec: 5, Total: 5, Updated: 5, Ready: 2,
			Generation: 4, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:      ScaleIntent{From: 5, To: 3},
		wantReason:  autoscalingv1alpha1.ReasonRolloutInProgress,
		wantMessage: msgRolloutInProgress,
	},
	{
		name: "settle holds once the rollout is done",
		state: WorkloadState{
			Spec: 5, Total: 5, Updated: 5, Ready: 3,
			Generation: 3, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent:      ScaleIntent{From: 5, To: 4},
		wantReason:  autoscalingv1alpha1.ReasonScaleDownSettling,
		wantMessage: msgScaleDownNotSettled,
	},
	{
		name: "no guard holds a settled scale-down",
		state: WorkloadState{
			Spec: 5, Total: 5, Updated: 5, Ready: 5,
			Generation: 3, ObservedGeneration: 3,
			TracksRollout: true,
		},
		intent: ScaleIntent{From: 5, To: 4},
	},
}

// workloadStateCases are readWorkloadState over each kind, then the missing
// and invalid-field defaults. A nil field is omitted. The four full-status
// rows share one set of numbers so the only per-kind difference is
// TracksRollout.
var workloadStateCases = []workloadStateCase{
	{
		name: "Deployment with full status",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: 4, ready: 3, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
			TracksRollout: true,
		},
	},
	{
		name: "StatefulSet with full status",
		kind: "StatefulSet",
		fields: workloadObjectFields{
			spec: 4, ready: 3, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
		},
	},
	{
		name: "LeaderWorkerSet with full status",
		kind: "LeaderWorkerSet",
		fields: workloadObjectFields{
			spec: 4, ready: 3, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
		},
	},
	{
		name: "generic kind with full status",
		kind: "ReplicaSet",
		fields: workloadObjectFields{
			spec: 4, ready: 3, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
		},
	},
	{
		name: "missing spec.replicas reads as 1",
		kind: deploymentKind,
		fields: workloadObjectFields{
			ready: 3, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 1, Total: 6, Updated: 2, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
			TracksRollout: true,
		},
	},
	{
		name: "spec.replicas of 0 is kept",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: 0, ready: 0, total: 0, updated: 0,
			generation: 2, observed: 2,
		},
		want: WorkloadState{
			Spec: 0, Generation: 2, ObservedGeneration: 2,
			TracksRollout: true,
		},
	},
	{
		name: "missing readyReplicas reads as 0",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: 4, total: 6, updated: 2, terminating: 1,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 0,
			Terminating: 1, TerminatingReported: true,
			Generation: 9, ObservedGeneration: 8,
			TracksRollout: true,
		},
	},
	{
		name: "missing terminatingReplicas is not reported",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: 4, ready: 3, total: 6, updated: 2,
			generation: 9, observed: 8,
		},
		want: WorkloadState{
			Spec: 4, Total: 6, Updated: 2, Ready: 3,
			Generation: 9, ObservedGeneration: 8,
			TracksRollout: true,
		},
	},
	{
		name: "missing generation, observedGeneration, updated, and total read as 0",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: 4, ready: 3, terminating: 1,
		},
		want: WorkloadState{
			Spec: 4, Ready: 3,
			Terminating: 1, TerminatingReported: true,
			TracksRollout: true,
		},
	},
	{
		name: "invalid-typed fields behave like missing",
		kind: deploymentKind,
		fields: workloadObjectFields{
			spec: "nope", ready: "nope", total: "nope", updated: "nope",
			terminating: "nope", generation: "nope", observed: "nope",
		},
		want: WorkloadState{Spec: 1, TracksRollout: true},
	},
}

// targetGuardCases are the guard result the controller turns into a
// condition, starting from a target object rather than a WorkloadState.
// readyReplicas is the raw count: 0 does not count as settled.
// gen/observed/updated/spec/ready build the object. status.replicas stays
// at 5 inside the builder. The runner parses that object and evaluates guards.
var targetGuardCases = []targetGuardCase{
	{
		name: "rollout holds a scale-up",
		gen:  4, observed: 3, updated: 5,
		kind: deploymentKind, spec: 5, ready: 5, target: 7,
		wantReason:  autoscalingv1alpha1.ReasonRolloutInProgress,
		wantMessage: msgRolloutInProgress,
	},
	{
		name: "rollout wins over an unsettled scale-down",
		gen:  4, observed: 3, updated: 5,
		kind: deploymentKind, spec: 5, ready: 2, target: 3,
		wantReason:  autoscalingv1alpha1.ReasonRolloutInProgress,
		wantMessage: msgRolloutInProgress,
	},
	{
		name: "unsettled scale-down",
		gen:  3, observed: 3, updated: 5, // fully rolled out
		kind: deploymentKind, spec: 5, ready: 3, target: 4,
		wantReason:  autoscalingv1alpha1.ReasonScaleDownSettling,
		wantMessage: msgScaleDownNotSettled,
	},
	{
		// readyReplicas 0 is also what a missing field reads as. Treating it
		// as spec would skip the guard.
		name: "ready count of 0 is not settled",
		gen:  3, observed: 3, updated: 5,
		kind: deploymentKind, spec: 5, ready: 0, target: 4,
		wantReason:  autoscalingv1alpha1.ReasonScaleDownSettling,
		wantMessage: msgScaleDownNotSettled,
	},
	{
		name: "settled scale-down may proceed",
		gen:  3, observed: 3, updated: 5,
		kind: deploymentKind, spec: 5, ready: 5, target: 4,
	},
	{
		name: "scale-up is not gated on settle",
		gen:  3, observed: 3, updated: 5,
		kind: deploymentKind, spec: 5, ready: 2, target: 6,
	},
	{
		name: "non-Deployment ignores the rollout guard",
		gen:  4, observed: 1, updated: 0,
		kind: "StatefulSet", spec: 5, ready: 5, target: 7,
	},
}
