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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	autoscalingv1alpha1 "github.com/modelsphere/llm-operator/api/autoscaling/v1alpha1"
)

// The reconcile specs are tables of scalerScenario values. The runner and the
// fixtures live in llmscaler_scenario_test.go; the rows live in
// llmscaler_controller_cases_test.go. Helper tests that do not need envtest
// are plain testing funcs in llmscaler_helpers_test.go.
var _ = Describe("LLMScaler Controller", func() {
	Context("When reconciling a resource", func() {
		BeforeEach(func() {
			promFix = startPromFixture()
		})
		AfterEach(func() {
			promFix.stop()
			promFix = nil
		})

		DescribeTable("metric scaling", runScalerScenario, metricScalingCases)
		DescribeTable("rollout guard (G8)", runScalerScenario, rolloutGuardCases)
		DescribeTable("settle guard (G7)", runScalerScenario, settleGuardCases)
		DescribeTable("stabilization and max step", runScalerScenario, stabilizationAndStepCases)
		DescribeTable("deferral timeout", runScalerScenario, deferralTimeoutCases)
		DescribeTable("deferral status", runScalerScenario, deferralStatusCases)
		DescribeTable("forced-write pacing", runScalerScenario, forcedWritePacingCases)

		It("should default deferTimeoutSeconds to 600", func() {
			scaler := &autoscalingv1alpha1.LLMScaler{}
			Expect(k8sClient.Get(promFix.ctx, promFix.scaler, scaler)).To(Succeed())
			// The fixture leaves the field unset; the CRD default fills it.
			Expect(scaler.Spec.DeferTimeoutSeconds).NotTo(BeNil())
			Expect(*scaler.Spec.DeferTimeoutSeconds).To(Equal(autoscalingv1alpha1.DefaultDeferTimeoutSeconds))
		})
	})

	// These run against the envtest apiserver so the CRD's own union rules are
	// exercised, not just the controller's reading of a well-formed spec.
	Context("When the metric provider is Custom", func() {
		BeforeEach(func() {
			customFix = startCustomFixture()
		})
		AfterEach(func() {
			customFix.stop()
			customFix = nil
		})

		DescribeTable("decision server", runCustomProviderReconcile, customProviderReconcileCases)
		DescribeTable("admission", runCustomProviderAdmission, customProviderAdmissionCases)

		It("should default metricProvider to Prometheus and customProvider.path to /decisions", func() {
			By("creating a scaler that leaves both unset")
			scaler := customFix.newScaler()
			scaler.Spec.MetricProvider = ""
			scaler.Spec.CustomProvider = nil
			scaler.Spec.Metrics = []autoscalingv1alpha1.MetricSpec{{Query: "avg(x)", Target: "1"}}
			Expect(k8sClient.Create(customFix.ctx, scaler)).To(Succeed())

			stored := &autoscalingv1alpha1.LLMScaler{}
			Expect(k8sClient.Get(customFix.ctx, customFix.scaler, stored)).To(Succeed())
			Expect(stored.Spec.MetricProvider).To(Equal(autoscalingv1alpha1.MetricProviderPrometheus))

			By("switching it to the Custom provider without naming a path")
			stored.Spec.MetricProvider = autoscalingv1alpha1.MetricProviderCustom
			stored.Spec.CustomProvider = &autoscalingv1alpha1.CustomProviderSpec{ServiceID: sampleServiceID}
			Expect(k8sClient.Update(customFix.ctx, stored)).To(Succeed())

			Expect(k8sClient.Get(customFix.ctx, customFix.scaler, stored)).To(Succeed())
			Expect(stored.Spec.CustomProvider.Path).To(Equal(defaultDecisionsPath))
		})
	})
})
