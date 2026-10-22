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

	. "github.com/onsi/ginkgo/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	autoscalingv1alpha1 "github.com/modelsphere/llm-operator/api/autoscaling/v1alpha1"
)

// metricScalingCases are plain metric-driven writes: scale up, down to min, to zero, wake from zero, and the ready-replica ratio fallback.
var metricScalingCases = []TableEntry{
	// ceil(1 * 0.85/0.5) = ceil(1.7) = 2. The mock sample is 0.85 and the target is 0.5.
	Entry("should successfully scale the deployment based on metrics", scalerScenario{
		plainReconciler: true,
		steps: []scenarioStep{
			{by: "running the Reconciler"},
			{by: "checking if the Deployment replicas increased", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(2))}},
		},
	}),

	// desired = ceil(5 * 0/0.5) = 0, clamped up to minReplicas (1).
	Entry("should scale down to minReplicas when the metric is zero", scalerScenario{
		plainReconciler: true,
		steps: []scenarioStep{
			{by: "setting the Deployment to 5 replicas and the metric to 0 (idle)", skipReconcile: true,
				metric: "0", // empty queue
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 5, available: 5}},
			{by: "running the Reconciler"},
			{by: "checking it scaled down to minReplicas", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),

	// desired = ceil(2 * 0/0.5) = 0, clamped to minReplicas (0).
	Entry("should scale down to zero when minReplicas is 0", scalerScenario{
		plainReconciler: true,
		prelude:         "lowering the scaler's minReplicas to 0",
		spec:            scalerSpecPatch{minReplicas: ptr(int32(0))},
		steps: []scenarioStep{
			{by: "setting the Deployment to 2 replicas, fully rolled out", skipReconcile: true,
				metric: "0", // idle -> wants zero
				deploy: &deployState{spec: ptr(int32(2)), replicas: 2, updated: 2, ready: 2, available: 2}},
			{by: "running the Reconciler"},
			{by: "checking it scaled down to zero", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(0))}},
		},
	}),

	// desired = ceil(1 * 0.85/0.5) = 2 against the single-replica baseline,
	// not ceil(0 * 0.85/0.5) = 0.
	Entry("should wake the fleet from zero when the metric turns positive", scalerScenario{
		plainReconciler: true,
		prelude:         "lowering the scaler's minReplicas to 0 and parking the Deployment at 0",
		spec:            scalerSpecPatch{minReplicas: ptr(int32(0))},
		// Load arrives while the fleet is at zero. mockMetricValue is already the fixture sample.
		deploy: &deployState{spec: ptr(int32(0)), replicas: 0, updated: 0, ready: 0, available: 0},
		steps: []scenarioStep{
			{by: "running the Reconciler"},
			{by: "checking the fleet woke up", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(2))}},
		},
	}),

	// readyReplicas is 0, so the ratio falls back to spec (3):
	// ceil(3 * 0.85/0.5) = 6, clamped to maxReplicas 5. Without that
	// fallback the single-replica baseline would recommend 2.
	// status.currentReplicas stays 0. ready 0 is omitted and reads as missing,
	// so available must be 0 too or the apiserver rejects the status.
	Entry("should keep the ready-replica fallback for the scaling ratio only", scalerScenario{
		plainReconciler: true,
		steps: []scenarioStep{
			{deploy: &deployState{spec: ptr(int32(3)), replicas: 3, updated: 3, ready: 0, available: 0},
				expect: stepExpect{
					replicas:      ptr(int32(5)),
					current:       ptr(int32(0)),
					desired:       ptr(int32(5)),
					applied:       ptr(int32(5)),
					deferredSince: sinceNil(),
					condition: &condExpect{
						status: metav1.ConditionFalse,
						reason: autoscalingv1alpha1.ReasonNotDeferred,
					},
				}},
		},
	}),
}

// rolloutGuardCases (G8) defer scaling while the Deployment is mid-rollout.
var rolloutGuardCases = []TableEntry{
	// The metric would compute desired=2, but the rollout guard defers
	// scaling, so replicas stay at 1.
	Entry("should defer scaling while the Deployment is rolling out", scalerScenario{
		plainReconciler: true,
		steps: []scenarioStep{
			{by: "marking the Deployment as mid-rollout", rollout: true, skipReconcile: true},
			{by: "running the Reconciler"},
			{by: "checking the Deployment was NOT scaled", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),
}

// settleGuardCases (G7) defer scale-down until the fleet is back at spec, and do not gate scale-up.
var settleGuardCases = []TableEntry{
	Entry("should defer a scale-down until readyReplicas is back at specReplicas", scalerScenario{
		plainReconciler: true,
		metric:          "0", // empty queue -> wants minReplicas
		steps: []scenarioStep{
			{by: "setting the Deployment to 5 replicas with only 3 ready",
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 3, available: 3}},
			{by: "checking the scale-down was deferred", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(5))}},
			{by: "letting the fleet converge",
				deploy: &deployState{replicas: 5, updated: 5, ready: 5, available: 5}},
			{by: "checking the scale-down now proceeds", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),

	// The fallback path. readyReplicas excludes terminating pods, so the
	// fleet reads as settled the moment the previous step's pods are marked
	// for deletion — the descent is then paced by syncPeriodSeconds, not by
	// the drain.
	Entry("should ignore still-draining pods while checkTerminatingReplicas is off", scalerScenario{
		plainReconciler:  true,
		checkTerminating: ptr(false),
		metric:           "0",
		steps: []scenarioStep{
			{by: "setting 5 ready with 2 still draining",
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 5, available: 5, terminating: ptr(int32(2))}},
			{by: "checking the scale-down was not held back by the drain", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),

	Entry("should also wait for the drain when checkTerminatingReplicas is on", scalerScenario{
		plainReconciler:  true,
		checkTerminating: ptr(true),
		metric:           "0",
		steps: []scenarioStep{
			{by: "setting 5 ready with 2 still draining",
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 5, available: 5, terminating: ptr(int32(2))}},
			{by: "checking it is deferred; readyReplicas alone would have said settled", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(5))}},
			{by: "letting the drain finish",
				deploy: &deployState{replicas: 5, updated: 5, ready: 5, available: 5, terminating: ptr(int32(0))}},
			{by: "checking the scale-down now proceeds", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),

	// ceil(2 * 0.85/0.5) = 4; the settle guard only gates scale-down.
	Entry("should not gate scale-up on the previous scale-down settling", scalerScenario{
		plainReconciler: true,
		steps: []scenarioStep{
			{by: "setting 3 replicas with 2 ready and 1 still terminating, under load",
				// Over the 0.5 target -> wants more replicas.
				metric: mockMetricValue,
				deploy: &deployState{spec: ptr(int32(3)), replicas: 3, updated: 3, ready: 2, available: 2, terminating: ptr(int32(1))}},
			{by: "checking capacity was added despite the unsettled fleet", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(4))}},
		},
	}),
}

// stabilizationAndStepCases hold scale-down for the stabilization window and walk down one maxStepReplicas at a time.
var stabilizationAndStepCases = []TableEntry{
	Entry("should hold for the stabilization window between scale-down steps", scalerScenario{
		plainReconciler: true,
		prelude:         "enabling a 300s window and a 2-replica step cap",
		spec: scalerSpecPatch{
			stabilizationWindowSeconds: ptr(int32(300)),
			maxStepReplicas:            ptr(int32(2)),
		},
		metric: "0",
		steps: []scenarioStep{
			{by: "starting settled at 5 with an idle metric",
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 5, available: 5}},
			{by: "checking the first step landed", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(3))}},
			{by: "settling the fleet at 3 so neither the rollout nor the settle guard fires",
				observeSpec: ptr(int32(3))},
			{by: "checking the next step is held; without the pin it would drop to 1", skipReconcile: true,
				expect: stepExpect{replicas: ptr(int32(3))}},
		},
	}),

	Entry("should walk down one step at a time when maxStepReplicas is set", scalerScenario{
		plainReconciler: true,
		prelude:         "capping the scale-down step at 2 replicas",
		spec:            scalerSpecPatch{maxStepReplicas: ptr(int32(2))},
		steps: []scenarioStep{
			{by: "setting the Deployment to 5 replicas and the metric to 0 (idle)", skipReconcile: true,
				metric: "0", // empty queue
				deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 5, available: 5}},
			// desired = 1 (clamped to minReplicas), capped to 5 - 2 = 3.
			{by: "running the Reconciler; raw recommendation is minReplicas but the step is capped",
				expect: stepExpect{replicas: ptr(int32(3))}},
			// The cap rate-limits the write; the recommendation is still 1.
			{by: "checking status reports the uncapped recommendation, not the step", skipReconcile: true,
				expect: stepExpect{desired: ptr(int32(1))}},
			// 3 - 2 = 1, which is minReplicas, so it lands there rather than below.
			{by: "running the Reconciler again; it takes the next step down",
				deploy: &deployState{spec: ptr(int32(3)), replicas: 3, updated: 3, ready: 3, available: 3},
				expect: stepExpect{replicas: ptr(int32(1))}},
		},
	}),
}

// deferralTimeoutCases force a held write once the deferral times out, including a restarted reconciler and a timeout of 0.
var deferralTimeoutCases = []TableEntry{
	Entry("should force a scale-up once a stuck rollout exceeds the deferral timeout", scalerScenario{
		spec:    scalerSpecPatch{deferTimeoutSeconds: ptr(int32(60))},
		rollout: true,
		steps: []scenarioStep{
			// Deadline is 60s out and the default sync period is 15s, so the
			// wait is the sync period. The recommendation is recorded even
			// though nothing was written.
			{by: "deferring the first sync and stamping deferredSince", at: 0,
				expect: stepExpect{
					requeueAfter:  ptr(15 * time.Second),
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(0),
					desired:       ptr(int32(2)),
					applied:       ptr(int32(1)),
					condition: &condExpect{
						status:                    metav1.ConditionTrue,
						reason:                    autoscalingv1alpha1.ReasonRolloutInProgress,
						observedGenerationCurrent: true,
						messageContains: []string{
							"holding 1 replicas (wanted 2)",
							msgRolloutInProgress,
							deferralStart.Format(time.RFC3339),
							"will apply after 60s",
						},
					},
					events: []eventExpect{{
						prefix:   "Normal " + eventReasonScalingDeferred,
						contains: []string{"holding 1 replicas (wanted 2)"},
					}},
				}},
			// One second remains until the deadline, under the sync period.
			{by: "reconciling again before the timeout, without restamping or rewriting",
				at: 59 * time.Second,
				expect: stepExpect{
					requeueAfter:  ptr(time.Second),
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(0),
					applied:       ptr(int32(1)),
					noMoreEvents:  true,
				}},
			// The rollout guard still matches after the write, so the clock
			// stays where the deferral started. The forced write itself is not
			// a hold, so the next sync is a full period.
			{by: "advancing past the timeout and forcing the held scale-up",
				at: 60 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(2)),
					deferredSince: sinceAt(0),
					desired:       ptr(int32(2)),
					applied:       ptr(int32(2)),
					requeueAfter:  ptr(15 * time.Second),
					condition: &condExpect{
						status:                    metav1.ConditionFalse,
						reason:                    autoscalingv1alpha1.ReasonDeferralTimedOut,
						observedGenerationCurrent: true,
						messageContains:           []string{"scaling 1 -> 2 anyway"},
					},
					events: []eventExpect{{
						prefix:   "Warning " + eventReasonDeferralTimedOut,
						contains: []string{msgRolloutInProgress, "1 -> 2"},
					}},
					noMoreEvents: true,
				}},
		},
	}),

	// readyReplicas == 0 is omitted from status, so the controller sees the
	// field as missing. A ready==0 -> spec fallback in the guard would have made
	// this look settled and scaled immediately; the settle guard now uses
	// the raw count. The ratio still falls back to spec, which is why an
	// idle metric recommends minReplicas rather than staying at 5.
	// available must be 0 too: the apiserver rejects availableReplicas > readyReplicas,
	// and 0 is omitted, so the controller sees that field as missing as well.
	Entry("should force a scale-down of an unready fleet once the deferral times out", scalerScenario{
		spec: scalerSpecPatch{
			deferTimeoutSeconds: ptr(int32(60)),
			maxStepReplicas:     ptr(int32(2)),
		},
		prelude: "capping the step and parking 5 replicas with none ready",
		deploy:  &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 0, available: 0},
		metric:  "0",
		steps: []scenarioStep{
			// Uncapped recommendation is minReplicas; the held write is the step.
			{at: 0,
				expect: stepExpect{
					replicas:      ptr(int32(5)),
					deferredSince: sinceAt(0),
					desired:       ptr(int32(1)),
					current:       ptr(int32(0)),
					applied:       ptr(int32(5)),
					condition: &condExpect{
						status: metav1.ConditionTrue,
						reason: autoscalingv1alpha1.ReasonScaleDownSettling,
						messageContains: []string{
							"holding 5 replicas (wanted 3)",
							"will apply after 60s",
						},
					},
				}},
			{by: "keeping the same deferral until the timeout", at: 59 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(5)),
					deferredSince: sinceAt(0),
				}},
			// None of the pods are ready, so the settle guard still matches and
			// the clock is not restarted for the next step.
			{by: "forcing the capped step, not the uncapped recommendation", at: 60 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(3)),
					deferredSince: sinceAt(0),
					desired:       ptr(int32(1)),
					applied:       ptr(int32(3)),
					current:       ptr(int32(0)),
					condition: &condExpect{
						status:          metav1.ConditionFalse,
						reason:          autoscalingv1alpha1.ReasonDeferralTimedOut,
						messageContains: []string{"scaling 5 -> 3 anyway"},
					},
					// The entering deferral event, then the timeout warning.
					events: []eventExpect{
						{prefix: "Normal " + eventReasonScalingDeferred},
						{prefix: "Warning " + eventReasonDeferralTimedOut, contains: []string{msgScaleDownNotSettled, "5 -> 3"}},
					},
				}},
		},
	}),

	// deferredSince lives in status, so a process restart (a new reconciler,
	// with an empty recommendation map) still enforces the deadline.
	// The rollout is still in progress, so the deadline carried in status is
	// what forced the write and it stays put.
	Entry("should force a deferral that already exceeded the timeout on a fresh reconciler", scalerScenario{
		spec:                     scalerSpecPatch{deferTimeoutSeconds: ptr(int32(60))},
		rollout:                  true,
		seedDeferredSince:        ptr(-2 * time.Hour),
		expectNilRecommendations: true,
		steps: []scenarioStep{
			{at: 0, expect: stepExpect{
				replicas:      ptr(int32(2)),
				deferredSince: sinceSeed(),
				applied:       ptr(int32(2)),
				condition: &condExpect{
					status: metav1.ConditionFalse,
					reason: autoscalingv1alpha1.ReasonDeferralTimedOut,
				},
				events: []eventExpect{{
					prefix:   "Warning " + eventReasonDeferralTimedOut,
					contains: []string{"1 -> 2"},
				}},
			}},
		},
	}),

	// Timeout 0 never forces, so the wait stays the sync period even though
	// the recorded deferral is already a day old.
	Entry("should never force a deferral when deferTimeoutSeconds is 0", scalerScenario{
		spec:              scalerSpecPatch{deferTimeoutSeconds: ptr(int32(0))},
		rollout:           true,
		seedDeferredSince: ptr(-24 * time.Hour),
		steps: []scenarioStep{
			{at: 0, expect: stepExpect{
				requeueAfter:  ptr(15 * time.Second),
				replicas:      ptr(int32(1)),
				deferredSince: sinceSeed(),
				condition: &condExpect{
					status: metav1.ConditionTrue,
					reason: autoscalingv1alpha1.ReasonRolloutInProgress,
					// Split so this is not a third copy of the controller's limit phrase.
					messageContains: []string{"never forced (" + "deferTimeoutSeconds is 0)"},
				},
				events:       []eventExpect{{prefix: "Normal " + eventReasonScalingDeferred}},
				noMoreEvents: true,
			}},
		},
	}),
}

// deferralStatusCases cover the condition, status, and event lifecycle of one deferral.
var deferralStatusCases = []TableEntry{
	// 5 replicas, 3 ready, but not yet rolled out: the rollout guard wins
	// over the settle guard. An idle metric wants to scale down.
	Entry("should keep one deferral when the guard reason changes", scalerScenario{
		spec:    scalerSpecPatch{deferTimeoutSeconds: ptr(int32(60))},
		metric:  "0",
		deploy:  &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 3, available: 3},
		rollout: true,
		steps: []scenarioStep{
			{at: 0, expect: stepExpect{
				deferredSince: sinceAt(0),
				condition:     &condExpect{reason: autoscalingv1alpha1.ReasonRolloutInProgress},
			}},
			{by: "finishing the rollout while the fleet is still short of ready",
				at:     10 * time.Second,
				deploy: &deployState{replicas: 5, updated: 5, ready: 3, available: 3},
				expect: stepExpect{
					replicas:      ptr(int32(5)),
					deferredSince: sinceAt(0),
					condition:     &condExpect{reason: autoscalingv1alpha1.ReasonScaleDownSettling},
					events: []eventExpect{
						{contains: []string{msgRolloutInProgress}},
						{contains: []string{msgScaleDownNotSettled}},
					},
				}},
			{by: "not emitting again while the same guard still defers",
				at: 10 * time.Second,
				expect: stepExpect{
					deferredSince: sinceAt(0),
					noMoreEvents:  true,
				}},
		},
	}),

	// ceil(1 * 0.5/0.5) = 1, which is already spec, and the rollout is done,
	// so no guard matches. The no-op ends the deferral.
	Entry("should clear a deferral on a no-op once no guard matches", scalerScenario{
		spec:    scalerSpecPatch{deferTimeoutSeconds: ptr(int32(60))},
		rollout: true,
		steps: []scenarioStep{
			{at: 0, expect: stepExpect{deferredSince: sinceAt(0)}},
			{by: "finishing the rollout at the replica count the metric already wants",
				at:          10 * time.Second,
				metric:      "0.5",
				observeSpec: ptr(int32(1)),
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceNil(),
					applied:       ptr(int32(1)),
					forcedWrite:   ptr(false),
					condition: &condExpect{
						status:          metav1.ConditionFalse,
						reason:          autoscalingv1alpha1.ReasonNotDeferred,
						messageContains: []string{"already at the step target"},
					},
				}},
		},
	}),

	Entry("should keep deferredSince across no-op syncs while a rollout guard still matches", scalerScenario{
		spec:    scalerSpecPatch{deferTimeoutSeconds: ptr(int32(60))},
		rollout: true,
		steps: []scenarioStep{
			// ceil(1 * 0.5/0.5) = 1, already spec. The guard matches, but there
			// is nothing to hold, so deferredSince stays nil.
			{by: "not starting a deferral on a no-op, even though the rollout guard matches",
				at: 0, metric: "0.5",
				expect: stepExpect{
					deferredSince: sinceNil(),
					replicas:      ptr(int32(1)),
					condition: &condExpect{
						status: metav1.ConditionFalse,
						reason: autoscalingv1alpha1.ReasonNotDeferred,
					},
				}},
			// ceil(1 * 0.85/0.5) = 2.
			{by: "stamping deferredSince on the first sync that actually wants to write",
				at: 10 * time.Second, metric: "0.85",
				expect: stepExpect{
					deferredSince: sinceAt(10 * time.Second),
					replicas:      ptr(int32(1)),
					condition: &condExpect{
						status: metav1.ConditionTrue,
						reason: autoscalingv1alpha1.ReasonRolloutInProgress,
					},
				}},
			// The no-ops must not move the clock. The stamp is deferralStart+10s.
			{by: "alternating a no-op with a held scale-up; the no-ops must not move the clock",
				at: 20 * time.Second, metric: "0.5",
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(10 * time.Second),
					condition: &condExpect{
						status: metav1.ConditionTrue,
						reason: autoscalingv1alpha1.ReasonRolloutInProgress,
						messageContains: []string{
							"nothing to write this sync",
							"guard is still active",
							"deferral clock keeps running",
							deferralStart.Add(10 * time.Second).Format(time.RFC3339),
							msgRolloutInProgress,
						},
					},
				}},
			{at: 30 * time.Second, metric: "0.85",
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(10 * time.Second),
					condition: &condExpect{
						status: metav1.ConditionTrue,
						reason: autoscalingv1alpha1.ReasonRolloutInProgress,
					},
				}},
			{at: 40 * time.Second, metric: "0.5",
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(10 * time.Second),
					condition: &condExpect{
						status: metav1.ConditionTrue,
						reason: autoscalingv1alpha1.ReasonRolloutInProgress,
						messageContains: []string{
							"nothing to write this sync",
							"guard is still active",
							"deferral clock keeps running",
							deferralStart.Add(10 * time.Second).Format(time.RFC3339),
							msgRolloutInProgress,
						},
					},
				}},
			{by: "forcing the held scale-up once the original deadline passes",
				at: 70 * time.Second, metric: "0.85",
				expect: stepExpect{
					replicas:      ptr(int32(2)),
					deferredSince: sinceAt(10 * time.Second),
					condition: &condExpect{
						status:          metav1.ConditionFalse,
						reason:          autoscalingv1alpha1.ReasonDeferralTimedOut,
						messageContains: []string{"scaling 1 -> 2 anyway"},
					},
				}},
		},
	}),
}

// forcedWritePacingCases pace a second forced step to one per sync period, and clear the pace when the guard lifts.
var forcedWritePacingCases = []TableEntry{
	Entry("should force the next stuck scale-down step after one sync period", scalerScenario{
		spec: scalerSpecPatch{
			deferTimeoutSeconds: ptr(int32(60)),
			syncPeriodSeconds:   ptr(int32(15)),
			maxStepReplicas:     ptr(int32(2)),
		},
		prelude: "capping the step and parking 5 replicas with none ready",
		deploy:  &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 0, available: 0},
		metric:  "0",
		steps: []scenarioStep{
			{at: 0,
				expect: stepExpect{
					replicas:      ptr(int32(5)),
					deferredSince: sinceAt(0),
					condition:     &condExpect{reason: autoscalingv1alpha1.ReasonScaleDownSettling},
				}},
			{by: "forcing the first step once the timeout elapses", at: 60 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(3)),
					deferredSince: sinceAt(0),
					forcedWrite:   ptr(true),
					forcedWriteAt: ptr(60 * time.Second),
				}},
			// The spec write bumped generation. Observe it with nothing ready
			// so the settle guard, not a phantom rollout, is what still matches.
			{by: "keeping the fleet unready and taking the next step one sync period later, without another timeout",
				at:          75 * time.Second,
				observeSpec: ptr(int32(0)),
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(0),
					applied:       ptr(int32(1)),
					condition: &condExpect{
						status: metav1.ConditionFalse,
						reason: autoscalingv1alpha1.ReasonDeferralTimedOut,
						messageContains: []string{
							"scaling 3 -> 1 anyway",
							msgScaleDownNotSettled,
						},
					},
				}},
		},
	}),

	Entry("should not take a second forced step until a sync period has elapsed", scalerScenario{
		spec: scalerSpecPatch{
			deferTimeoutSeconds: ptr(int32(60)),
			syncPeriodSeconds:   ptr(int32(15)),
			maxStepReplicas:     ptr(int32(2)),
		},
		metric: "0",
		deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 0, available: 0},
		steps: []scenarioStep{
			// Snapshot the pre-write status. The next step is the forced write
			// this By describes.
			{by: "forcing one scale-down step of an unready fleet", at: 0, snapshotStatus: true},
			{at: 60 * time.Second, expect: stepExpect{replicas: ptr(int32(3))}},
			// The target write stuck at 3. The status write that would have
			// recorded it did not, so a retry still sees the deadline passed.
			{by: "putting the status back to its pre-write state, as if that update had conflicted",
				restoreStatus: true, skipReconcile: true},
			{by: "reconciling again with the clock unchanged, which must not take the next step",
				at: 60 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(3)),
					requeueAfter:  ptr(15 * time.Second),
					deferredSince: sinceAt(0),
					applied:       ptr(int32(3)),
					condition: &condExpect{
						status:          metav1.ConditionTrue,
						messageContains: []string{"forced write paced to one per sync period"},
					},
				}},
			{by: "still holding one second short of the sync period",
				at: 74 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(3)),
					requeueAfter:  ptr(time.Second),
					deferredSince: sinceAt(0),
				}},
			{by: "forcing the next step once the sync period has elapsed",
				at: 75 * time.Second,
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceAt(0),
					applied:       ptr(int32(1)),
				}},
		},
	}),

	Entry("should clear deferredSince and the forced-write pace when no guard matches", scalerScenario{
		spec: scalerSpecPatch{
			deferTimeoutSeconds: ptr(int32(60)),
			maxStepReplicas:     ptr(int32(2)),
		},
		metric: "0",
		deploy: &deployState{spec: ptr(int32(5)), replicas: 5, updated: 5, ready: 0, available: 0},
		steps: []scenarioStep{
			{at: 0},
			{at: 60 * time.Second, expect: stepExpect{
				replicas:      ptr(int32(3)),
				forcedWrite:   ptr(true),
				deferredSince: sinceAt(0),
			}},
			// Settled and fully observed: the next scale-down is an ordinary
			// write, not a forced one, even though the pace has not elapsed.
			{by: "letting the fleet become ready at the replica count just written",
				at:          60 * time.Second,
				observeSpec: ptr(int32(3)),
				expect: stepExpect{
					replicas:      ptr(int32(1)),
					deferredSince: sinceNil(),
					forcedWrite:   ptr(false),
					condition: &condExpect{
						status:          metav1.ConditionFalse,
						reason:          autoscalingv1alpha1.ReasonNotDeferred,
						messageContains: []string{"scaled 3 -> 1"},
					},
				}},
		},
	}),
}

// customProviderReconcileCases scale from a decision server, and hold replicas when that server cannot be reached.
var customProviderReconcileCases = []TableEntry{
	// spec.metrics is required only under the Prometheus provider, so a
	// Custom scaler must be accepted without it. active 4 is an absolute
	// count, so 1 ready replica scales straight to 4 rather than through
	// the per-replica HPA ratio.
	Entry("should scale the Deployment to the count the decision server reports", customReconcileCase{
		createBy:       "creating an LLMScaler with no metrics at all",
		reconcileBy:    "running the Reconciler",
		replicasBy:     "checking the Deployment landed on the decision, not on a ratio of it",
		wantReplicas:   4,
		statusBy:       "checking the status reports the same recommendation",
		wantDesired:    ptr(int32(4)),
		decisionStatus: metav1.ConditionTrue,
	}),

	// Above minReplicas on purpose: at 1 replica "held" and "collapsed to
	// minReplicas" are the same number, so the test would pass either way.
	Entry("should hold replicas when the decision server is unreachable", customReconcileCase{
		deploy:                  &deployState{spec: ptr(int32(3)), replicas: 3, updated: 3, ready: 3, available: 3},
		deployBy:                "putting the Deployment at 3 replicas, above minReplicas",
		closeServer:             true,
		closeBy:                 "taking the decision server down",
		reconcileBy:             "running the Reconciler",
		replicasBy:              "checking the Deployment was left where it was, not collapsed to minReplicas",
		wantReplicas:            3,
		statusBy:                "checking the failure is reported on the status",
		decisionStatus:          metav1.ConditionFalse,
		decisionReason:          autoscalingv1alpha1.ReasonDecisionUnavailable,
		decisionMessageNonEmpty: true,
	}),
}

// customProviderAdmissionCases are the CRD union rules for the metric provider.
var customProviderAdmissionCases = []TableEntry{
	Entry("should reject a Custom scaler with no customProvider", customAdmissionCase{
		omitCustomProvider: true,
		wantErr:            "spec.customProvider is required",
	}),
	Entry("should reject a Prometheus scaler with no metrics", customAdmissionCase{
		metricProvider:     autoscalingv1alpha1.MetricProviderPrometheus,
		omitCustomProvider: true,
		wantErr:            "spec.metrics must be non-empty",
	}),
}
