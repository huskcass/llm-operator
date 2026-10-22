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

package serving

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	servingv1alpha1 "github.com/modelsphere/llm-operator/api/serving/v1alpha1"
	"github.com/modelsphere/llm-operator/internal/helm"
	"github.com/modelsphere/llm-operator/internal/testutil/cases"
)

// serviceCase is one LLMService controller spec. HistoryLimit defaults to 10.
// PendingTimeout zero (omitted) leaves the controller default.
type serviceCase struct {
	Name           string          `json:"name,omitempty"`
	Description    string          `json:"description,omitempty"`
	HistoryLimit   int             `json:"historyLimit,omitempty"`
	PendingTimeout *cases.Duration `json:"pendingTimeout,omitempty"`
	Steps          []serviceStep   `json:"steps"`
}

type serviceStep struct {
	Action         string            `json:"action"`
	FreshNamespace bool              `json:"freshNamespace,omitempty"`
	Name           string            `json:"name,omitempty"`
	Version        string            `json:"version,omitempty"`
	Annotations    map[string]string `json:"annotations,omitempty"`
	Finalizers     []string          `json:"finalizers,omitempty"`
	ChartVersion   *string           `json:"chartVersion,omitempty"`
	Suspend        *bool             `json:"suspend,omitempty"`
	Layers         []layerSpec       `json:"layers,omitempty"`
	Release        *releaseSpec      `json:"release,omitempty"`
	Entries        []releaseSpec     `json:"entries,omitempty"`
	Put            string            `json:"put,omitempty"`
	Versions       []string          `json:"versions,omitempty"`
	ListErr        *string           `json:"listErr,omitempty"`
	InstallErr     *string           `json:"installErr,omitempty"`
	UpgradeErr     *string           `json:"upgradeErr,omitempty"`
	GetErr         *string           `json:"getErr,omitempty"`
	KeepFailed     *bool             `json:"keepFailed,omitempty"`
	Repos          []string          `json:"repos,omitempty"`
	Status         *statusSpec       `json:"status,omitempty"`
	Capture        []string          `json:"capture,omitempty"`
	Expect         *expectSpec       `json:"expect,omitempty"`
}

type layerSpec struct {
	Name   string `json:"name"`
	Values string `json:"values,omitempty"`
}

type releaseSpec struct {
	Revision     int               `json:"revision,omitempty"`
	Status       string            `json:"status,omitempty"`
	ChartName    string            `json:"chartName,omitempty"`
	ChartVersion string            `json:"chartVersion,omitempty"`
	Config       map[string]any    `json:"config,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Owned        bool              `json:"owned,omitempty"`
	Slot         bool              `json:"slot,omitempty"`
	Updated      *cases.Duration   `json:"updated,omitempty"`
}

type statusSpec struct {
	Phase      string          `json:"phase,omitempty"`
	Conditions []conditionSpec `json:"conditions,omitempty"`
}

type conditionSpec struct {
	Type               string `json:"type"`
	Status             string `json:"status,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	ObservedGeneration string `json:"observedGeneration,omitempty"`
	Absent             bool   `json:"absent,omitempty"`
}

type expectSpec struct {
	Mid                  *expectSpec      `json:"mid,omitempty"`
	Error                *bool            `json:"error,omitempty"`
	ErrorContains        string           `json:"errorContains,omitempty"`
	Installs             *int             `json:"installs,omitempty"`
	Upgrades             *int             `json:"upgrades,omitempty"`
	Gets                 *int             `json:"gets,omitempty"`
	Uninstalls           *int             `json:"uninstalls,omitempty"`
	Rollbacks            *int             `json:"rollbacks,omitempty"`
	LastRollback         *int             `json:"lastRollback,omitempty"`
	HelmTouched          *int             `json:"helmTouched,omitempty"`
	ListCalls            *int             `json:"listCalls,omitempty"`
	LastForce            *bool            `json:"lastForce,omitempty"`
	GetsUnchanged        *bool            `json:"getsUnchanged,omitempty"`
	UpgradesUnchanged    *bool            `json:"upgradesUnchanged,omitempty"`
	Phase                string           `json:"phase,omitempty"`
	ChartVersion         string           `json:"chartVersion,omitempty"`
	ReResolve            *string          `json:"reResolve,omitempty"`
	AppliedHash          *hashExpect      `json:"appliedHash,omitempty"`
	HelmRevision         *int             `json:"helmRevision,omitempty"`
	ObservedGeneration   string           `json:"observedGeneration,omitempty"`
	Generation           *int64           `json:"generation,omitempty"`
	History              *historyExpect   `json:"history,omitempty"`
	Conditions           []conditionSpec  `json:"conditions,omitempty"`
	ReleaseUID           string           `json:"releaseUID,omitempty"`
	Release              *releaseExpect   `json:"release,omitempty"`
	NotFound             *bool            `json:"notFound,omitempty"`
	Message              *messageExpect   `json:"message,omitempty"`
	DistinctRevisions    *int             `json:"distinctRevisions,omitempty"`
	Snapshots            []snapshotExpect `json:"snapshots,omitempty"`
	Requeue              *requeueExpect   `json:"requeue,omitempty"`
	EventContains        string           `json:"eventContains,omitempty"`
	Finalizer            string           `json:"finalizer,omitempty"`
	PlantedRevisionUsed  *bool            `json:"plantedRevisionUsed,omitempty"`
	CapturedRevisionGone *bool            `json:"capturedRevisionGone,omitempty"`
	LatestRevisionExists *bool            `json:"latestRevisionExists,omitempty"`
}

type hashExpect struct {
	Prefix   string `json:"prefix,omitempty"`
	Empty    *bool  `json:"empty,omitempty"`
	Captured *bool  `json:"captured,omitempty"`
}

type historyExpect struct {
	Length  *int                 `json:"length,omitempty"`
	Entries []historyEntryExpect `json:"entries,omitempty"`
}

type historyEntryExpect struct {
	Index              int      `json:"index"`
	Action             string   `json:"action,omitempty"`
	Revision           *int     `json:"revision,omitempty"`
	AnnotationsPresent []string `json:"annotationsPresent,omitempty"`
	AnnotationsAbsent  []string `json:"annotationsAbsent,omitempty"`
	ReResolved         *bool    `json:"reResolved,omitempty"`
	ForceConflicts     *bool    `json:"forceConflicts,omitempty"`
	HashEqualsApplied  bool     `json:"hashEqualsApplied,omitempty"`
	HashEqualsIndex    *int     `json:"hashEqualsIndex,omitempty"`
}

type releaseExpect struct {
	Exists       *bool             `json:"exists,omitempty"`
	Status       string            `json:"status,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	LabelsAbsent []string          `json:"labelsAbsent,omitempty"`
}

type messageExpect struct {
	Contains    []string `json:"contains,omitempty"`
	Equals      string   `json:"equals,omitempty"`
	NotContains []string `json:"notContains,omitempty"`
}

type snapshotExpect struct {
	Index             int               `json:"index"`
	Annotations       map[string]string `json:"annotations,omitempty"`
	AnnotationsAbsent []string          `json:"annotationsAbsent,omitempty"`
	ChartName         string            `json:"chartName,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	ControllerOwner   *bool             `json:"controllerOwner,omitempty"`
}

type requeueExpect struct {
	Exact   *cases.Duration `json:"exact,omitempty"`
	AtLeast *cases.Duration `json:"atLeast,omitempty"`
	Above   *cases.Duration `json:"above,omitempty"`
	AtMost  *cases.Duration `json:"atMost,omitempty"`
}

type helmSnap struct {
	gets     int
	upgrades int
}

type caseRun struct {
	tc                 serviceCase
	ns                 string
	name               string
	h                  *fakeHelm
	l                  *versionList
	r                  *LLMServiceReconciler
	events             chan string
	capturedRevision   string
	appliedHash        string
	observedGeneration int64
	planted            string
}

func runServiceCase(tc serviceCase) {
	rec := events.NewFakeRecorder(32)
	h := &fakeHelm{}
	l := &versionList{versions: []string{ver086}}
	limit := tc.HistoryLimit
	if limit == 0 {
		limit = 10
	}
	r := newReconciler(h, l, limit)
	r.Recorder = rec
	if tc.PendingTimeout != nil {
		r.PendingTimeout = tc.PendingTimeout.Std()
	}
	run := &caseRun{
		tc:     tc,
		ns:     takeNS(),
		name:   "qwen",
		h:      h,
		l:      l,
		r:      r,
		events: rec.Events,
	}
	Expect(tc.Steps).NotTo(BeEmpty(), tc.Name)
	for i, step := range tc.Steps {
		run.step(i, step)
	}
}

func (c *caseRun) step(i int, step serviceStep) {
	if step.FreshNamespace {
		c.ns = takeNS()
	}
	if step.Name != "" {
		c.name = step.Name
	}
	if step.Action == "" {
		Fail(fmt.Sprintf("%s: step %d has no action", c.tc.Name, i+1))
	}
	if stepRequiresExpect(step.Action) && step.Expect == nil {
		Fail(fmt.Sprintf("%s: step %d (%s) missing expect", c.tc.Name, i+1, step.Action))
	}
	switch step.Action {
	case "create":
		c.create(step)
	case "reconcile":
		c.reconcile(step.Expect)
	case "update":
		c.update(step)
	case "delete":
		c.deleteObj()
	case "putRelease":
		c.putRelease(step)
	case "setVersions":
		c.setVersions(step)
	case "setHelm":
		c.setHelm(step)
	case "setAllowed":
		// An omitted list is nil, and a nil allowlist permits every repository.
		if step.Repos == nil {
			Fail(fmt.Sprintf("%s: step %d setAllowed requires repos", c.tc.Name, i+1))
		}
		c.r.AllowedChartRepos = append([]string(nil), step.Repos...)
	case "deleteRelease":
		delete(c.h.releases, c.h.slot(c.ns, c.name))
	case "setHistory":
		c.setHistory(step)
	case "plantStale":
		c.plantStale()
	case "reconcileHeld":
		c.reconcileHeld(step.Expect)
	case "capture":
		c.capture(step)
	case "setStatus":
		c.setStatus(step)
	case "setStatusReleaseUID":
		c.setStatusReleaseUID()
	default:
		Fail(fmt.Sprintf("%s: step %d unknown action %q", c.tc.Name, i+1, step.Action))
	}
	if step.Expect != nil && step.Action != "reconcile" && step.Action != "reconcileHeld" {
		c.check(step.Expect, reconcile.Result{}, nil, helmSnap{}, false)
	}
}

func stepRequiresExpect(action string) bool {
	return action == "reconcile" || action == "reconcileHeld"
}

func (c *caseRun) create(step serviceStep) {
	Expect(step.Version).NotTo(BeEmpty())
	obj := newService(c.ns, c.name, step.Version)
	maps.Copy(obj.Annotations, step.Annotations)
	if step.Finalizers != nil {
		obj.Finalizers = append([]string(nil), step.Finalizers...)
	}
	Expect(k8sClient.Create(ctx, obj)).To(Succeed())
}

func (c *caseRun) update(step serviceStep) {
	obj := fetch(c.ns, c.name)
	if step.Annotations != nil {
		if obj.Annotations == nil {
			obj.Annotations = map[string]string{}
		}
		maps.Copy(obj.Annotations, step.Annotations)
	}
	if step.ChartVersion != nil {
		obj.Spec.Chart.Version = *step.ChartVersion
	}
	if step.Suspend != nil {
		obj.Spec.Suspend = *step.Suspend
	}
	if step.Layers != nil {
		obj.Spec.Layers = layersFrom(step.Layers)
	}
	Expect(k8sClient.Update(ctx, obj)).To(Succeed())
}

func layersFrom(in []layerSpec) []servingv1alpha1.Layer {
	out := make([]servingv1alpha1.Layer, len(in))
	for i, l := range in {
		out[i] = servingv1alpha1.Layer{Name: l.Name}
		if l.Values != "" {
			out[i].Values = &apiextensionsv1.JSON{Raw: []byte(l.Values)}
		}
	}
	return out
}

func (c *caseRun) deleteObj() {
	Expect(k8sClient.Delete(ctx, fetch(c.ns, c.name))).To(Succeed())
}

func (c *caseRun) reconcile(exp *expectSpec) {
	before := helmSnap{gets: c.h.gets, upgrades: c.h.upgrades}
	result, err := doReconcile(c.r, c.ns, c.name)
	c.check(exp, result, err, before, true)
}

// reconcileHeld runs one reconcile that blocks inside helm, checks the
// mid-apply status, then lets the install finish.
func (c *caseRun) reconcileHeld(exp *expectSpec) {
	Expect(exp).NotTo(BeNil())
	Expect(exp.Mid).NotTo(BeNil())
	c.h.hold = make(chan struct{})
	c.h.entered = make(chan struct{}, 1)
	released := false
	release := func() {
		if !released {
			close(c.h.hold)
			released = true
		}
	}
	defer release()

	done := make(chan error, 1)
	go func() {
		defer GinkgoRecover()
		_, recErr := doReconcile(c.r, c.ns, c.name)
		done <- recErr
	}()
	Eventually(c.h.entered).WithTimeout(5 * time.Second).Should(Receive())
	c.check(exp.Mid, reconcile.Result{}, nil, helmSnap{}, false)
	release()
	var recErr error
	Eventually(done).WithTimeout(5 * time.Second).Should(Receive(&recErr))
	Expect(recErr).NotTo(HaveOccurred())
	c.check(exp, reconcile.Result{}, recErr, helmSnap{}, true)
}

func (c *caseRun) putRelease(step serviceStep) {
	Expect(step.Release).NotTo(BeNil())
	rel := c.releaseFrom(*step.Release)
	c.h.put(c.ns, c.name, &rel)
}

func (c *caseRun) setHistory(step serviceStep) {
	Expect(step.Entries).NotTo(BeEmpty())
	hist := make([]helm.Release, len(step.Entries))
	for i, e := range step.Entries {
		hist[i] = c.releaseFrom(e)
	}
	c.h.histories = map[string][]helm.Release{c.h.slot(c.ns, c.name): hist}
	if step.Put == "" {
		return
	}
	Expect(step.Put).To(Equal("last"))
	last := hist[len(hist)-1]
	c.h.put(c.ns, c.name, &last)
}

func (c *caseRun) releaseFrom(in releaseSpec) helm.Release {
	if in.Slot {
		cur := c.h.releases[c.h.slot(c.ns, c.name)]
		Expect(cur).NotTo(BeNil())
		return *cur
	}
	if in.Owned {
		updated := time.Now()
		if in.Updated != nil {
			updated = time.Now().Add(in.Updated.Std())
		}
		return *ownedRel(fetch(c.ns, c.name), in.Revision, in.Status, updated)
	}
	rel := helm.Release{
		Revision:     in.Revision,
		Status:       in.Status,
		ChartName:    in.ChartName,
		ChartVersion: in.ChartVersion,
		Config:       in.Config,
		Labels:       in.Labels,
	}
	if in.Updated != nil {
		rel.Updated = time.Now().Add(in.Updated.Std())
	}
	return rel
}

func (c *caseRun) setVersions(step serviceStep) {
	if step.Versions != nil {
		c.l.versions = append([]string(nil), step.Versions...)
	}
	if step.ListErr != nil {
		c.l.err = errFrom(*step.ListErr)
	}
}

func (c *caseRun) setHelm(step serviceStep) {
	if step.InstallErr != nil {
		c.h.installErr = errFrom(*step.InstallErr)
	}
	if step.UpgradeErr != nil {
		c.h.upgradeErr = errFrom(*step.UpgradeErr)
	}
	if step.GetErr != nil {
		c.h.getErr = errFrom(*step.GetErr)
	}
	if step.KeepFailed != nil {
		c.h.keepFailed = *step.KeepFailed
	}
}

func errFrom(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}

func (c *caseRun) setStatus(step serviceStep) {
	Expect(step.Status).NotTo(BeNil())
	obj := fetch(c.ns, c.name)
	if step.Status.Phase != "" {
		obj.Status.Phase = servingv1alpha1.Phase(step.Status.Phase)
	}
	for _, cond := range step.Status.Conditions {
		meta.SetStatusCondition(&obj.Status.Conditions, conditionFrom(obj, cond))
	}
	Expect(k8sClient.Status().Update(ctx, obj)).To(Succeed())
}

func conditionFrom(obj *servingv1alpha1.LLMService, cond conditionSpec) metav1.Condition {
	out := metav1.Condition{
		Type:    cond.Type,
		Status:  metav1.ConditionStatus(cond.Status),
		Reason:  cond.Reason,
		Message: cond.Message,
	}
	switch cond.ObservedGeneration {
	case "":
	case "current":
		out.ObservedGeneration = obj.Generation
	default:
		Fail("unsupported observedGeneration " + cond.ObservedGeneration)
	}
	return out
}

func (c *caseRun) setStatusReleaseUID() {
	obj := fetch(c.ns, c.name)
	obj.Status.ReleaseUID = string(obj.UID)
	Expect(k8sClient.Status().Update(ctx, obj)).To(Succeed())
}

func (c *caseRun) capture(step serviceStep) {
	Expect(step.Capture).NotTo(BeEmpty())
	obj := fetch(c.ns, c.name)
	for _, what := range step.Capture {
		switch what {
		case "observedGeneration":
			c.observedGeneration = obj.Status.ObservedGeneration
		case "appliedHash":
			c.appliedHash = obj.Status.AppliedHash
		case "revision":
			Expect(obj.Status.History).NotTo(BeEmpty())
			c.capturedRevision = obj.Status.History[0].ControllerRevision
		default:
			Fail("unknown capture " + what)
		}
	}
}

// plantStale leaves a ControllerRevision under the name the next install
// reuses, owned by the service, so the reconcile replaces that snapshot.
func (c *caseRun) plantStale() {
	obj := fetch(c.ns, c.name)
	values, err := mergeLayers(obj.Spec.Layers)
	Expect(err).NotTo(HaveOccurred())
	hash, err := appliedHash(chartName, ver086, values)
	Expect(err).NotTo(HaveOccurred())
	stale := &appsv1.ControllerRevision{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllerRevisionName(obj.Name, hash, 1),
			Namespace: c.ns,
		},
		Data:     runtime.RawExtension{Raw: []byte(`{"annotations":{"swiss.modelsphere.dev/action":"old"}}`)},
		Revision: 1,
	}
	Expect(controllerutil.SetControllerReference(obj, stale, k8sClient.Scheme())).To(Succeed())
	Expect(k8sClient.Create(ctx, stale)).To(Succeed())
	c.planted = stale.Name
}

func (c *caseRun) check(exp *expectSpec, result reconcile.Result, err error, before helmSnap, reconciled bool) {
	if exp == nil {
		return
	}
	c.checkErr(exp, err, reconciled)
	c.checkRequeue(exp.Requeue, result)
	c.checkHelm(exp, before)
	if exp.EventContains != "" {
		Expect(c.events).To(Receive(ContainSubstring(exp.EventContains)))
	}
	if exp.NotFound != nil && *exp.NotFound {
		c.checkMissing()
	}
	var got *servingv1alpha1.LLMService
	if exp.wantsObject() {
		got = fetch(c.ns, c.name)
		c.checkObject(exp, got)
	}
	if exp.Release != nil {
		c.checkRelease(exp.Release, got)
	}
}

func (c *caseRun) checkErr(exp *expectSpec, err error, reconciled bool) {
	if exp.ErrorContains != "" {
		Expect(err).To(MatchError(ContainSubstring(exp.ErrorContains)))
		return
	}
	if exp.Error != nil {
		if *exp.Error {
			Expect(err).To(HaveOccurred())
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
		return
	}
	if reconciled {
		Expect(err).NotTo(HaveOccurred())
	}
}

func (c *caseRun) checkRequeue(exp *requeueExpect, result reconcile.Result) {
	if exp == nil {
		return
	}
	if exp.Exact != nil {
		Expect(result.RequeueAfter).To(Equal(exp.Exact.Std()))
	}
	if exp.AtLeast != nil {
		Expect(result.RequeueAfter).To(BeNumerically(">=", exp.AtLeast.Std()))
	}
	if exp.Above != nil {
		Expect(result.RequeueAfter).To(BeNumerically(">", exp.Above.Std()))
	}
	if exp.AtMost != nil {
		Expect(result.RequeueAfter).To(BeNumerically("<=", exp.AtMost.Std()))
	}
}

func (c *caseRun) checkHelm(exp *expectSpec, before helmSnap) {
	expectInt(c.h.installs, exp.Installs)
	expectInt(c.h.upgrades, exp.Upgrades)
	expectInt(c.h.gets, exp.Gets)
	expectInt(c.h.uninstalls, exp.Uninstalls)
	expectInt(c.h.rollbacks, exp.Rollbacks)
	expectInt(c.h.lastRollback, exp.LastRollback)
	expectInt(c.l.calls, exp.ListCalls)
	if exp.HelmTouched != nil {
		Expect(c.h.gets + c.h.installs + c.h.upgrades).To(Equal(*exp.HelmTouched))
	}
	expectBool(c.h.lastForce, exp.LastForce)
	if exp.GetsUnchanged != nil && *exp.GetsUnchanged {
		Expect(c.h.gets).To(Equal(before.gets))
	}
	if exp.UpgradesUnchanged != nil && *exp.UpgradesUnchanged {
		Expect(c.h.upgrades).To(Equal(before.upgrades))
	}
}

func expectInt(got int, want *int) {
	if want != nil {
		Expect(got).To(Equal(*want))
	}
}

func expectBool(got bool, want *bool) {
	if want == nil {
		return
	}
	if *want {
		Expect(got).To(BeTrue())
	} else {
		Expect(got).To(BeFalse())
	}
}

func (e *expectSpec) wantsObject() bool {
	if e.Phase != "" || e.ChartVersion != "" || e.ReResolve != nil || e.AppliedHash != nil {
		return true
	}
	if e.HelmRevision != nil || e.ObservedGeneration != "" || e.Generation != nil || e.History != nil {
		return true
	}
	if len(e.Conditions) > 0 || e.ReleaseUID != "" || e.Message != nil || e.Finalizer != "" {
		return true
	}
	if e.DistinctRevisions != nil || len(e.Snapshots) > 0 || e.PlantedRevisionUsed != nil {
		return true
	}
	return e.CapturedRevisionGone != nil || e.LatestRevisionExists != nil
}

func (c *caseRun) checkObject(exp *expectSpec, got *servingv1alpha1.LLMService) {
	if exp.Phase != "" {
		Expect(got.Status.Phase).To(Equal(servingv1alpha1.Phase(exp.Phase)))
	}
	if exp.ChartVersion != "" {
		Expect(got.Status.Chart.Version).To(Equal(exp.ChartVersion))
	}
	if exp.ReResolve != nil {
		Expect(got.Status.Chart.ReResolve).To(Equal(*exp.ReResolve))
	}
	c.checkHash(exp.AppliedHash, got)
	if exp.HelmRevision != nil {
		Expect(got.Status.Helm.Revision).To(Equal(*exp.HelmRevision))
	}
	c.checkGeneration(exp, got)
	c.checkHistory(exp, got)
	c.checkConditions(exp.Conditions, got)
	c.checkReleaseUID(exp.ReleaseUID, got)
	c.checkMessage(exp.Message, got)
	c.checkFinalizer(exp.Finalizer, got)
	c.checkRevisions(exp, got)
}

func (c *caseRun) checkHash(exp *hashExpect, got *servingv1alpha1.LLMService) {
	if exp == nil {
		return
	}
	if exp.Empty != nil && *exp.Empty {
		Expect(got.Status.AppliedHash).To(BeEmpty())
	}
	if exp.Prefix != "" {
		Expect(got.Status.AppliedHash).To(HavePrefix(exp.Prefix))
	}
	if exp.Captured != nil && *exp.Captured {
		Expect(got.Status.AppliedHash).To(Equal(c.appliedHash))
	}
}

func (c *caseRun) checkGeneration(exp *expectSpec, got *servingv1alpha1.LLMService) {
	if exp.Generation != nil {
		Expect(got.Generation).To(Equal(*exp.Generation))
	}
	switch exp.ObservedGeneration {
	case "":
	case "current":
		Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
	case "captured":
		Expect(got.Status.ObservedGeneration).To(Equal(c.observedGeneration))
	case "nonzero":
		Expect(got.Status.ObservedGeneration).NotTo(BeZero())
	default:
		Fail("unsupported observedGeneration " + exp.ObservedGeneration)
	}
}

func (c *caseRun) checkHistory(exp *expectSpec, got *servingv1alpha1.LLMService) {
	if exp.History == nil {
		return
	}
	if exp.History.Length != nil {
		Expect(got.Status.History).To(HaveLen(*exp.History.Length))
	}
	for _, e := range exp.History.Entries {
		Expect(e.Index).To(BeNumerically(">=", 0))
		Expect(e.Index).To(BeNumerically("<", len(got.Status.History)))
		entry := got.Status.History[e.Index]
		if e.Action != "" {
			Expect(entry.Action).To(Equal(e.Action))
		}
		if e.Revision != nil {
			Expect(entry.Revision).To(Equal(*e.Revision))
		}
		for _, k := range e.AnnotationsPresent {
			Expect(entry.Annotations).To(HaveKey(k))
		}
		for _, k := range e.AnnotationsAbsent {
			Expect(entry.Annotations).NotTo(HaveKey(k))
		}
		expectBool(entry.ReResolved, e.ReResolved)
		expectBool(entry.ForceConflicts, e.ForceConflicts)
		if e.HashEqualsApplied {
			Expect(entry.Hash).To(Equal(got.Status.AppliedHash))
		}
		if e.HashEqualsIndex != nil {
			Expect(entry.Hash).To(Equal(got.Status.History[*e.HashEqualsIndex].Hash))
		}
	}
	if exp.DistinctRevisions != nil {
		names := map[string]struct{}{}
		for _, entry := range got.Status.History {
			names[entry.ControllerRevision] = struct{}{}
		}
		Expect(names).To(HaveLen(*exp.DistinctRevisions))
	}
}

func (c *caseRun) checkConditions(conds []conditionSpec, got *servingv1alpha1.LLMService) {
	for _, want := range conds {
		gotCond := condition(got, want.Type)
		if want.Absent {
			Expect(gotCond).To(BeNil())
			continue
		}
		Expect(gotCond).NotTo(BeNil())
		if want.Status != "" {
			Expect(gotCond.Status).To(Equal(metav1.ConditionStatus(want.Status)))
		}
		if want.Reason != "" {
			Expect(gotCond.Reason).To(Equal(want.Reason))
		}
		if want.Message != "" {
			Expect(gotCond.Message).To(Equal(want.Message))
		}
		switch want.ObservedGeneration {
		case "":
		case "current":
			Expect(gotCond.ObservedGeneration).To(Equal(got.Generation))
		default:
			Fail("unsupported observedGeneration " + want.ObservedGeneration)
		}
	}
}

func (c *caseRun) checkReleaseUID(want string, got *servingv1alpha1.LLMService) {
	switch want {
	case "":
	case "self":
		Expect(got.Status.ReleaseUID).To(Equal(string(got.UID)))
	case "empty":
		Expect(got.Status.ReleaseUID).To(BeEmpty())
	default:
		Fail("unsupported releaseUID " + want)
	}
}

func (c *caseRun) checkMessage(exp *messageExpect, got *servingv1alpha1.LLMService) {
	if exp == nil {
		return
	}
	if exp.Equals != "" {
		Expect(got.Status.Message).To(Equal(exp.Equals))
	}
	// An empty substring matches every message, so it is rejected on its own.
	for _, sub := range exp.Contains {
		if sub == "" {
			Fail("message.contains entry is empty")
		}
		Expect(got.Status.Message).To(ContainSubstring(sub))
	}
	for _, sub := range exp.NotContains {
		if sub == "" {
			Fail("message.notContains entry is empty")
		}
		Expect(got.Status.Message).NotTo(ContainSubstring(sub))
	}
}

func (c *caseRun) checkFinalizer(want string, got *servingv1alpha1.LLMService) {
	switch want {
	case "":
	case "present":
		Expect(got.Finalizers).To(ContainElement(servingv1alpha1.Finalizer))
	case "absent":
		Expect(got.Finalizers).NotTo(ContainElement(servingv1alpha1.Finalizer))
	default:
		Fail("unsupported finalizer " + want)
	}
}

func (c *caseRun) checkRevisions(exp *expectSpec, got *servingv1alpha1.LLMService) {
	if exp.PlantedRevisionUsed != nil && *exp.PlantedRevisionUsed {
		Expect(got.Status.History).NotTo(BeEmpty())
		Expect(got.Status.History[0].ControllerRevision).To(Equal(c.planted))
	}
	if exp.CapturedRevisionGone != nil && *exp.CapturedRevisionGone {
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: c.ns, Name: c.capturedRevision}, &appsv1.ControllerRevision{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	}
	if exp.LatestRevisionExists != nil && *exp.LatestRevisionExists {
		Expect(got.Status.History).NotTo(BeEmpty())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: c.ns, Name: got.Status.History[0].ControllerRevision}, &appsv1.ControllerRevision{})).To(Succeed())
	}
	for _, sn := range exp.Snapshots {
		Expect(sn.Index).To(BeNumerically("<", len(got.Status.History)))
		c.checkSnapshot(got.Status.History[sn.Index].ControllerRevision, sn)
	}
}

func (c *caseRun) checkSnapshot(name string, sn snapshotExpect) {
	var cr appsv1.ControllerRevision
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: c.ns, Name: name}, &cr)).To(Succeed())
	if sn.ControllerOwner != nil && *sn.ControllerOwner {
		Expect(cr.OwnerReferences).NotTo(BeEmpty())
		Expect(cr.OwnerReferences[0].Controller).NotTo(BeNil())
		Expect(*cr.OwnerReferences[0].Controller).To(BeTrue())
	}
	for k, v := range sn.Labels {
		Expect(cr.Labels).To(HaveKeyWithValue(k, v))
	}
	var snap revisionSnapshot
	Expect(json.Unmarshal(cr.Data.Raw, &snap)).To(Succeed())
	for k, v := range sn.Annotations {
		Expect(snap.Annotations).To(HaveKeyWithValue(k, v))
	}
	for _, k := range sn.AnnotationsAbsent {
		Expect(snap.Annotations).NotTo(HaveKey(k))
	}
	if sn.ChartName != "" {
		Expect(snap.Spec.Chart.Name).To(Equal(sn.ChartName))
	}
}

func (c *caseRun) checkRelease(exp *releaseExpect, got *servingv1alpha1.LLMService) {
	slot := c.h.slot(c.ns, c.name)
	rel := c.h.releases[slot]
	if exp.Exists != nil {
		if *exp.Exists {
			Expect(c.h.releases).To(HaveKey(slot))
		} else {
			Expect(c.h.releases).NotTo(HaveKey(slot))
		}
	}
	if exp.Status != "" {
		Expect(rel).NotTo(BeNil())
		Expect(rel.Status).To(Equal(exp.Status))
	}
	if len(exp.Labels) == 0 && len(exp.LabelsAbsent) == 0 {
		return
	}
	Expect(rel).NotTo(BeNil())
	for k, v := range exp.Labels {
		if v == "self" {
			if got == nil {
				got = fetch(c.ns, c.name)
			}
			v = string(got.UID)
		}
		Expect(rel.Labels).To(HaveKeyWithValue(k, v))
	}
	for _, k := range exp.LabelsAbsent {
		Expect(rel.Labels).NotTo(HaveKey(k))
	}
}

func (c *caseRun) checkMissing() {
	err := k8sClient.Get(ctx, types.NamespacedName{Namespace: c.ns, Name: c.name}, &servingv1alpha1.LLMService{})
	Expect(apierrors.IsNotFound(err)).To(BeTrue())
}
