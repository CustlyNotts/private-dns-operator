/*
Copyright 2026 private-dns-operator authors

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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	"github.com/custlynotts/private-dns-operator/internal/coredns"
	dnsrender "github.com/custlynotts/private-dns-operator/internal/dns"
)

const privateDNSZoneFinalizer = "dns.custlynotts.io/private-zone-cleanup"

// Reasons reported on zone and record conditions.
const (
	reasonRendered            = "Rendered"
	reasonInvalidZone         = "InvalidZone"
	reasonInvalidRecordSet    = "InvalidRecordSet"
	reasonZoneRenderFailed    = "ZoneRenderFailed"
	reasonAccepted            = "Accepted"
	reasonZoneNotFound        = "ZoneNotFound"
	reasonZoneNotReady        = "ZoneNotReady"
	reasonNamespaceNotAllowed = "NamespaceNotAllowed"
	reasonInvalidRecord       = "InvalidRecord"
	reasonCoreDNSUnavailable  = "CoreDNSUnavailable"
)

// PrivateDNSZoneReconciler renders every PrivateDNSZone and PrivateDNSRecord in
// the cluster into the managed block of a CoreDNS Corefile.
//
// Reconcile is deliberately whole-world: it rebuilds the managed output from
// every live CR rather than mutating individual DNS lines, so deleting a record
// cannot leave a stale answer behind.
type PrivateDNSZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	CoreDNSNamespace  string
	CoreDNSConfigMap  string
	CoreDNSDeployment string
}

// zoneResult is the per-zone outcome of a reconcile, applied to status once the
// CoreDNS write has either succeeded or been skipped.
type zoneResult struct {
	conditions      []metav1.Condition
	records         int
	serial          string
	zoneFileKey     string
	lastAppliedHash string
	rendered        bool
}

// recordOutcome is the per-record outcome of a reconcile.
type recordOutcome struct {
	fqdn     string
	accepted bool
	reason   string
	message  string
}

func (r *PrivateDNSZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("rebuilding managed CoreDNS state", "trigger", req.Name)

	var zones dnsv1alpha1.PrivateDNSZoneList
	if err := r.List(ctx, &zones); err != nil {
		return ctrl.Result{}, err
	}

	var records dnsv1alpha1.PrivateDNSRecordList
	if err := r.List(ctx, &records); err != nil {
		return ctrl.Result{}, err
	}

	var cm corev1.ConfigMap
	cmErr := r.Get(ctx, types.NamespacedName{Namespace: r.CoreDNSNamespace, Name: r.CoreDNSConfigMap}, &cm)
	corefile := ""
	if cmErr == nil {
		corefile = cm.Data["Corefile"]
	}

	// A CoreDNS target that is provably absent means the operator holds no state
	// it could ever clean up. Keeping a finalizer on a terminating zone in that
	// situation makes the zone permanently undeletable, and takes the CRD with
	// it, so release first and report the misconfiguration afterwards.
	switch {
	case apierrors.IsNotFound(cmErr):
		message := fmt.Sprintf("CoreDNS ConfigMap %s/%s does not exist", r.CoreDNSNamespace, r.CoreDNSConfigMap)
		return r.reportCoreDNSUnavailable(ctx, zones.Items, records.Items, message, true)

	case cmErr == nil && corefile == "":
		message := fmt.Sprintf("CoreDNS ConfigMap %s/%s has no Corefile key", r.CoreDNSNamespace, r.CoreDNSConfigMap)
		return r.reportCoreDNSUnavailable(ctx, zones.Items, records.Items, message, true)

	case cmErr != nil:
		// The target could not be read at all, for example because RBAC does not
		// cover this ConfigMap name. The managed block may still be in place, so
		// the finalizer has to hold. Say why on status instead of failing with no
		// explanation on any object.
		return r.reportCoreDNSUnavailable(ctx, zones.Items, records.Items, cmErr.Error(), false)
	}

	// Captured before ApplyConfigMap, which overwrites the annotation, and
	// before the Corefile is replaced. Both are needed to hold a failing zone's
	// last known good output and to avoid unmounting keys we never created.
	previousBlocks := coredns.ExtractZoneBlocks(corefile)
	previousManagedKeys := coredns.ManagedZoneKeys(&cm)

	// Claim every live zone before rendering. Update refreshes the object in
	// place, so the status patches later in this pass still see a current
	// resourceVersion and no requeue round-trip is needed.
	for i := range zones.Items {
		zone := &zones.Items[i]
		if !zone.DeletionTimestamp.IsZero() || containsString(zone.Finalizers, privateDNSZoneFinalizer) {
			continue
		}
		zone.Finalizers = append(zone.Finalizers, privateDNSZoneFinalizer)
		if err := r.Update(ctx, zone); err != nil {
			return ctrl.Result{}, err
		}
	}

	zoneFiles := map[string]string{}
	var directives []coredns.ZoneDirective
	zoneResults := map[string]zoneResult{}
	outcomes := newRecordOutcomes(records.Items)

	for i := range zones.Items {
		zone := &zones.Items[i]
		if !zone.DeletionTimestamp.IsZero() {
			continue
		}

		normalizedZone, err := dnsrender.NormalizeZone(zone.Spec.Zone)
		if err != nil {
			// The zone suffix itself is unusable, so there is no key to hold a
			// previous rendering under. The zone contributes nothing.
			zoneResults[zone.Name] = failedResult(zone.Generation, reasonInvalidZone, err.Error())
			markZoneRecordsNotReady(outcomes, records.Items, zone.Name, err.Error())
			continue
		}

		compiled, validationErrs := r.recordsForZone(*zone, normalizedZone, records.Items, outcomes)
		if len(validationErrs) > 0 {
			result := failedResult(zone.Generation, reasonInvalidRecordSet, validationErrs[0].Error())
			r.holdLastKnownGood(zone, normalizedZone, previousBlocks, previousManagedKeys, &cm, &result, &directives, zoneFiles)
			zoneResults[zone.Name] = result
			markZoneRecordsNotReady(outcomes, records.Items, zone.Name, validationErrs[0].Error())
			logger.Info("zone has an invalid record set, holding previous output",
				"zone", zone.Name, "error", validationErrs[0].Error())
			continue
		}

		zoneFile, err := dnsrender.RenderZone(*zone, compiled, time.Now())
		if err != nil {
			result := failedResult(zone.Generation, reasonZoneRenderFailed, err.Error())
			r.holdLastKnownGood(zone, normalizedZone, previousBlocks, previousManagedKeys, &cm, &result, &directives, zoneFiles)
			zoneResults[zone.Name] = result
			markZoneRecordsNotReady(outcomes, records.Items, zone.Name, err.Error())
			logger.Info("zone failed to render, holding previous output",
				"zone", zone.Name, "error", err.Error())
			continue
		}

		policy := effectivePolicy(zone.Spec.UnresolvedRecordPolicy)
		directives = append(directives, coredns.ZoneDirective{
			Zone:    normalizedZone,
			DBFile:  zoneFile.Key,
			Policy:  policy,
			Records: compiled,
		})

		result := zoneResult{
			records:         renderedRecordCount(compiled),
			serial:          zoneFile.Serial,
			lastAppliedHash: zoneFile.Hash,
			rendered:        true,
		}
		if policy == dnsv1alpha1.UnresolvedRecordPolicyForward {
			result.conditions = append(result.conditions,
				condition(ConditionTemplateRendered, metav1.ConditionTrue, reasonRendered,
					"CoreDNS template records rendered successfully", zone.Generation))
		} else {
			zoneFiles[zoneFile.Key] = zoneFile.Content
			result.zoneFileKey = zoneFile.Key
			result.conditions = append(result.conditions,
				condition(ConditionZoneFileRendered, metav1.ConditionTrue, reasonRendered,
					"zone file rendered successfully", zone.Generation))
		}
		zoneResults[zone.Name] = result
	}

	patchedCorefile, err := coredns.PatchCorefile(corefile, directives)
	if err != nil {
		return ctrl.Result{}, err
	}
	reloadable := coredns.HasReloadPlugin(patchedCorefile)

	if err := r.commitCoreDNS(ctx, &cm, patchedCorefile, zoneFiles, previousManagedKeys, reloadable, combinedHash(zoneResults)); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.releaseDeletedZones(ctx, zones.Items); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.writeZoneStatuses(ctx, zones.Items, zoneResults, reloadable); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.writeRecordStatuses(ctx, records.Items, outcomes); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// commitCoreDNS writes the rendered state to CoreDNS in an order that keeps both
// of the ConfigMap-volume invariants true at every step.
//
// CoreDNS mounts its ConfigMap with explicit volume items, so a key named in
// those items must exist in the ConfigMap, and a file named by the Corefile must
// be mounted in the container. Creating a zone needs the ConfigMap written
// first; removing one needs the Deployment written first. The sequence below
// satisfies both by expanding before it switches and contracting afterwards:
//
//  1. ConfigMap: add the rendered zone files, keeping stale keys and the old
//     Corefile. Nothing references the new keys yet.
//  2. Deployment: mount the union of old and new keys. Every file either
//     Corefile could name is now present and mounted.
//  3. ConfigMap: write the new Corefile. It only names mounted files.
//  4. Deployment: unmount the stale keys, which nothing names any more.
//  5. ConfigMap: delete the stale keys, which nothing mounts any more.
//
// The managed-keys annotation is widened in step 1 and narrowed only in step 5,
// so a reconcile that dies at any point leaves the orphans discoverable. Each
// step is skipped when it would be a no-op, which makes a steady-state
// reconcile write nothing at all.
func (r *PrivateDNSZoneReconciler) commitCoreDNS(
	ctx context.Context,
	cm *corev1.ConfigMap,
	corefile string,
	zoneFiles map[string]string,
	previousManagedKeys []string,
	reloadable bool,
	restartHash string,
) error {
	logger := log.FromContext(ctx)

	desiredKeys := mapKeys(zoneFiles)
	staleKeys := subtractKeys(previousManagedKeys, desiredKeys)
	mountAllKeys := unionKeys(previousManagedKeys, desiredKeys)

	// Step 1: expand the ConfigMap.
	changed := coredns.SetZoneFiles(cm, zoneFiles)
	if coredns.SetManagedZoneKeys(cm, mountAllKeys) {
		changed = true
	}
	if changed {
		if err := r.Update(ctx, cm); err != nil {
			return err
		}
	}

	// Step 2: mount the union, so the old and the new Corefile are both valid.
	dep, mountChanged, err := r.setVolumeItems(ctx, mountAllKeys, previousManagedKeys, false, "")
	if err != nil {
		return err
	}

	// Step 3: switch the Corefile over.
	corefileChanged := coredns.SetCorefile(cm, corefile)
	if corefileChanged {
		if err := r.Update(ctx, cm); err != nil {
			return err
		}
	}

	// Step 4: contract the Deployment. A rollout restart is only needed when the
	// Corefile changed and CoreDNS cannot reload it by itself; a volume item
	// change rolls the pods on its own, because it alters the pod template.
	restart := corefileChanged && !reloadable
	if _, _, err := r.setVolumeItems(ctx, desiredKeys, previousManagedKeys, restart, restartHash); err != nil {
		return err
	}

	// Step 5: contract the ConfigMap, now that nothing mounts the stale keys.
	changed = coredns.RemoveZoneFiles(cm, staleKeys)
	if coredns.SetManagedZoneKeys(cm, desiredKeys) {
		changed = true
	}
	if changed {
		if err := r.Update(ctx, cm); err != nil {
			return err
		}
	}

	if len(staleKeys) > 0 || mountChanged {
		logger.V(1).Info("reconciled CoreDNS zone files",
			"mounted", desiredKeys, "removed", staleKeys, "deployment", dep.Name)
	}
	return nil
}

// setVolumeItems reads the CoreDNS Deployment, sets the mounted zone file keys
// to mountKeys, optionally stamps a rollout restart, and writes it back only if
// something changed. The Deployment is re-read on every call so the two passes
// in commitCoreDNS cannot conflict with each other.
func (r *PrivateDNSZoneReconciler) setVolumeItems(
	ctx context.Context,
	mountKeys []string,
	previousManagedKeys []string,
	restart bool,
	restartHash string,
) (*appsv1.Deployment, bool, error) {
	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.CoreDNSNamespace, Name: r.CoreDNSDeployment}, &dep); err != nil {
		return nil, false, err
	}
	changed, err := coredns.EnsureZoneVolumeItems(&dep, r.CoreDNSConfigMap, mountKeys, previousManagedKeys)
	if err != nil {
		return nil, false, err
	}
	if restart {
		coredns.MarkRolloutRestart(&dep, "private-dns-zone-change", restartHash)
		changed = true
	}
	if changed {
		if err := r.Update(ctx, &dep); err != nil {
			return nil, false, err
		}
	}
	return &dep, changed, nil
}

// holdLastKnownGood carries a failing zone's previously rendered output forward
// so an invalid edit does not take the zone out of DNS. Zones that have never
// rendered successfully have nothing to hold and are simply absent from the
// managed block.
func (r *PrivateDNSZoneReconciler) holdLastKnownGood(
	zone *dnsv1alpha1.PrivateDNSZone,
	normalizedZone string,
	previousBlocks map[string]string,
	previousManagedKeys []string,
	cm *corev1.ConfigMap,
	result *zoneResult,
	directives *[]coredns.ZoneDirective,
	zoneFiles map[string]string,
) {
	raw, ok := previousBlocks[strings.TrimSuffix(normalizedZone, ".")]
	if !ok {
		return
	}

	*directives = append(*directives, coredns.ZoneDirective{Zone: normalizedZone, Raw: raw})
	result.conditions = append(result.conditions,
		condition(ConditionLastKnownGoodApplied, metav1.ConditionTrue, result.failureReason(),
			"kept the previously rendered output for this zone", zone.Generation))

	// Only re-claim a zone file we previously owned, so a key someone else put
	// in the ConfigMap is neither adopted nor deleted.
	key := dnsrender.ZoneFileKey(normalizedZone)
	if !containsString(previousManagedKeys, key) {
		return
	}
	existing, ok := cm.Data[key]
	if !ok {
		return
	}
	zoneFiles[key] = existing
	result.zoneFileKey = key
}

// reportCoreDNSUnavailable records why nothing could be rendered on every live
// zone and its records, and optionally releases terminating zones when the
// CoreDNS target is provably absent and there is therefore nothing to clean up.
//
// It always returns a non-nil error so the reconcile is retried with backoff.
func (r *PrivateDNSZoneReconciler) reportCoreDNSUnavailable(
	ctx context.Context,
	zones []dnsv1alpha1.PrivateDNSZone,
	records []dnsv1alpha1.PrivateDNSRecord,
	message string,
	releaseTerminating bool,
) (ctrl.Result, error) {
	if releaseTerminating {
		if err := r.releaseDeletedZones(ctx, zones); err != nil {
			return ctrl.Result{}, err
		}
	}

	results := make(map[string]zoneResult, len(zones))
	outcomes := newRecordOutcomes(records)
	for i := range zones {
		zone := &zones[i]
		if !zone.DeletionTimestamp.IsZero() {
			continue
		}
		results[zone.Name] = failedResult(zone.Generation, reasonCoreDNSUnavailable, message)
		markZoneRecordsNotReady(outcomes, records, zone.Name, message)
	}

	if err := r.writeZoneStatuses(ctx, zones, results, false); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.writeRecordStatuses(ctx, records, outcomes); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, errors.New(message)
}

func (r *PrivateDNSZoneReconciler) releaseDeletedZones(ctx context.Context, zones []dnsv1alpha1.PrivateDNSZone) error {
	for i := range zones {
		zone := &zones[i]
		if zone.DeletionTimestamp.IsZero() || !containsString(zone.Finalizers, privateDNSZoneFinalizer) {
			continue
		}
		zone.Finalizers = removeString(zone.Finalizers, privateDNSZoneFinalizer)
		if err := r.Update(ctx, zone); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *PrivateDNSZoneReconciler) writeZoneStatuses(
	ctx context.Context,
	zones []dnsv1alpha1.PrivateDNSZone,
	results map[string]zoneResult,
	reloadable bool,
) error {
	for i := range zones {
		zone := &zones[i]
		result, ok := results[zone.Name]
		if !ok || !zone.DeletionTimestamp.IsZero() {
			continue
		}

		desired := append([]metav1.Condition(nil), result.conditions...)
		if result.rendered {
			desired = append(desired,
				condition(ConditionCorefilePatched, metav1.ConditionTrue, "Patched",
					"CoreDNS Corefile managed block patched", zone.Generation),
				condition(ConditionVolumeMounted, metav1.ConditionTrue, "Mounted",
					"CoreDNS ConfigMap volume items are up to date", zone.Generation),
				condition(ConditionReloadTriggered, metav1.ConditionTrue, reloadReason(reloadable),
					reloadMessage(reloadable), zone.Generation),
				condition(ConditionReady, metav1.ConditionTrue, "Ready",
					"private DNS zone is ready", zone.Generation),
			)
		}

		status := dnsv1alpha1.PrivateDNSZoneStatus{
			ObservedGeneration: zone.Generation,
			Records:            result.records,
			Serial:             result.serial,
			ZoneFileKey:        result.zoneFileKey,
			LastAppliedHash:    result.lastAppliedHash,
			Conditions:         applyConditions(zone.Status.Conditions, desired, zoneConditionTypes),
		}
		if equality.Semantic.DeepEqual(zone.Status, status) {
			continue
		}

		patch := client.MergeFrom(zone.DeepCopy())
		zone.Status = status
		if err := r.Status().Patch(ctx, zone, patch); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *PrivateDNSZoneReconciler) writeRecordStatuses(
	ctx context.Context,
	records []dnsv1alpha1.PrivateDNSRecord,
	outcomes map[string]recordOutcome,
) error {
	for i := range records {
		record := &records[i]
		if !record.DeletionTimestamp.IsZero() {
			continue
		}
		outcome, ok := outcomes[recordKey(record.Namespace, record.Name)]
		if !ok {
			continue
		}

		conditionStatus := metav1.ConditionFalse
		if outcome.accepted {
			conditionStatus = metav1.ConditionTrue
		}
		status := dnsv1alpha1.PrivateDNSRecordStatus{
			ObservedGeneration: record.Generation,
			FQDN:               outcome.fqdn,
			Conditions: applyConditions(record.Status.Conditions, []metav1.Condition{
				condition(ConditionAccepted, conditionStatus, outcome.reason, outcome.message, record.Generation),
			}, []string{ConditionAccepted}),
		}
		if equality.Semantic.DeepEqual(record.Status, status) {
			continue
		}

		patch := client.MergeFrom(record.DeepCopy())
		record.Status = status
		if err := r.Status().Patch(ctx, record, patch); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// recordsForZone compiles the zone's inline records together with every
// PrivateDNSRecord that references it, recording a per-record outcome as it goes
// so each record object can report its own fate.
//
// The two sources are held to different standards on purpose.
//
// Inline records are part of the zone's own spec, so an invalid one makes the
// zone's desired state invalid and is returned as a blocking error: the zone
// holds its last known good output rather than publishing a spec its author got
// wrong.
//
// Delegated PrivateDNSRecord objects are admitted individually. One that is
// barred by allowedNamespaces or is malformed is excluded from the render and
// marked rejected on its own status, and the zone carries on. Treating those as
// zone-level failures would let any namespace that is explicitly *denied* access
// freeze the zone for everyone, turning the delegation boundary into a denial of
// service against itself.
//
// Only an inconsistency between records that were actually admitted, such as a
// CNAME sharing a name with other data, blocks the zone.
func (r *PrivateDNSZoneReconciler) recordsForZone(
	zone dnsv1alpha1.PrivateDNSZone,
	normalizedZone string,
	external []dnsv1alpha1.PrivateDNSRecord,
	outcomes map[string]recordOutcome,
) ([]dnsrender.Record, []error) {
	var admitted []dnsrender.Record
	var blocking []error

	zoneTTL := dnsrender.DefaultTTL
	if zone.Spec.TTL != nil {
		zoneTTL = *zone.Spec.TTL
	}

	for _, inline := range zone.Spec.Records {
		ttl := zoneTTL
		if inline.TTL != nil {
			ttl = *inline.TTL
		}
		fqdn, err := dnsrender.FQDN(inline.Name, normalizedZone)
		if err != nil {
			blocking = append(blocking, err)
			continue
		}
		record := dnsrender.Record{
			Zone: normalizedZone, Name: inline.Name, FQDN: fqdn, Type: inline.Type, TTL: ttl, Values: inline.Values,
			Source: dnsrender.Source{Kind: "PrivateDNSZone", Name: zone.Name},
		}
		if errs := dnsrender.ValidateRecord(record); len(errs) > 0 {
			blocking = append(blocking, errs...)
			continue
		}
		admitted = append(admitted, record)
	}

	for i := range external {
		candidate := &external[i]
		if candidate.Spec.ZoneRef.Name != zone.Name {
			continue
		}
		key := recordKey(candidate.Namespace, candidate.Name)

		if !namespaceAllowed(zone.Spec.AllowedNamespaces, candidate.Namespace) {
			outcomes[key] = recordOutcome{
				reason:  reasonNamespaceNotAllowed,
				message: fmt.Sprintf("namespace %q is not listed in zone %q allowedNamespaces", candidate.Namespace, zone.Name),
			}
			continue
		}

		ttl := zoneTTL
		if candidate.Spec.TTL != nil {
			ttl = *candidate.Spec.TTL
		}
		fqdn, err := dnsrender.FQDN(candidate.Spec.Name, normalizedZone)
		if err != nil {
			outcomes[key] = recordOutcome{reason: reasonInvalidRecord, message: err.Error()}
			continue
		}

		record := dnsrender.Record{
			Zone: normalizedZone, Name: candidate.Spec.Name, FQDN: fqdn, Type: candidate.Spec.Type, TTL: ttl, Values: candidate.Spec.Values,
			Source: dnsrender.Source{Kind: "PrivateDNSRecord", Namespace: candidate.Namespace, Name: candidate.Name},
		}
		if errs := dnsrender.ValidateRecord(record); len(errs) > 0 {
			outcomes[key] = recordOutcome{reason: reasonInvalidRecord, message: validationMessage(errs[0])}
			continue
		}

		admitted = append(admitted, record)
		outcomes[key] = recordOutcome{
			fqdn:     strings.TrimSuffix(fqdn, "."),
			accepted: true,
			reason:   reasonAccepted,
			message:  fmt.Sprintf("record is published in zone %q", zone.Name),
		}
	}

	setErrs := dnsrender.ValidateRecordSet(admitted)
	for _, err := range setErrs {
		var validationErr dnsrender.ValidationError
		if !errors.As(err, &validationErr) || validationErr.Source.Kind != "PrivateDNSRecord" {
			continue
		}
		key := recordKey(validationErr.Source.Namespace, validationErr.Source.Name)
		outcome := outcomes[key]
		outcome.accepted = false
		outcome.reason = reasonInvalidRecord
		outcome.message = validationErr.Message
		outcomes[key] = outcome
	}
	blocking = append(blocking, setErrs...)

	return admitted, blocking
}

// validationMessage unwraps the human-readable half of a record validation
// error, so a record's status explains the problem without repeating its own
// name back at it.
func validationMessage(err error) string {
	var validationErr dnsrender.ValidationError
	if errors.As(err, &validationErr) {
		return validationErr.Message
	}
	return err.Error()
}

func (r *PrivateDNSZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("privatednszone").
		For(&dnsv1alpha1.PrivateDNSZone{}).
		Watches(&dnsv1alpha1.PrivateDNSRecord{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
			record, ok := obj.(*dnsv1alpha1.PrivateDNSRecord)
			if !ok {
				return nil
			}
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: record.Spec.ZoneRef.Name}}}
		})).
		Complete(r)
}

// newRecordOutcomes seeds every record as rejected with ZoneNotFound. Records
// whose zone exists are overwritten while that zone is processed, so what
// remains are the records pointing at a zone that is absent or being deleted.
func newRecordOutcomes(records []dnsv1alpha1.PrivateDNSRecord) map[string]recordOutcome {
	outcomes := make(map[string]recordOutcome, len(records))
	for i := range records {
		record := &records[i]
		outcomes[recordKey(record.Namespace, record.Name)] = recordOutcome{
			reason:  reasonZoneNotFound,
			message: fmt.Sprintf("no PrivateDNSZone named %q is available", record.Spec.ZoneRef.Name),
		}
	}
	return outcomes
}

// markZoneRecordsNotReady reports a zone-level failure on the zone's records,
// leaving any record that already has a specific rejection reason of its own.
func markZoneRecordsNotReady(outcomes map[string]recordOutcome, records []dnsv1alpha1.PrivateDNSRecord, zoneName string, message string) {
	for i := range records {
		record := &records[i]
		if record.Spec.ZoneRef.Name != zoneName {
			continue
		}
		key := recordKey(record.Namespace, record.Name)
		if outcome, ok := outcomes[key]; ok && !outcome.accepted && outcome.reason != reasonZoneNotFound {
			continue
		}
		outcomes[key] = recordOutcome{
			reason:  reasonZoneNotReady,
			message: fmt.Sprintf("zone %q is not ready: %s", zoneName, message),
		}
	}
}

func recordKey(namespace string, name string) string {
	return namespace + "/" + name
}

func failedResult(generation int64, reason string, message string) zoneResult {
	return zoneResult{
		conditions: []metav1.Condition{
			condition(ConditionReady, metav1.ConditionFalse, reason, message, generation),
		},
	}
}

// failureReason returns the reason of the Ready=False condition, used to label
// why the previous output is being held.
func (z zoneResult) failureReason() string {
	for _, cond := range z.conditions {
		if cond.Type == ConditionReady && cond.Status == metav1.ConditionFalse {
			return cond.Reason
		}
	}
	return reasonInvalidRecordSet
}

func effectivePolicy(policy dnsv1alpha1.UnresolvedRecordPolicy) dnsv1alpha1.UnresolvedRecordPolicy {
	if policy == "" {
		return dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN
	}
	return policy
}

func namespaceAllowed(selector dnsv1alpha1.NamespaceSelector, namespace string) bool {
	if len(selector.MatchNames) == 0 {
		return true
	}
	return containsString(selector.MatchNames, namespace)
}

func renderedRecordCount(records []dnsrender.Record) int {
	count := 0
	for _, record := range records {
		count += len(record.Values)
	}
	return count
}

func reloadReason(reloadable bool) string {
	if reloadable {
		return "ReloadPlugin"
	}
	return "RolloutRestart"
}

func reloadMessage(reloadable bool) string {
	if reloadable {
		return "CoreDNS reload plugin will pick up the change"
	}
	return "CoreDNS deployment rollout restart was triggered because reload plugin is missing"
}

func combinedHash(results map[string]zoneResult) string {
	hashes := make([]string, 0, len(results))
	for _, result := range results {
		if result.lastAppliedHash != "" {
			hashes = append(hashes, result.lastAppliedHash)
		}
	}
	sort.Strings(hashes)
	return strings.Join(hashes, ",")
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// subtractKeys returns the members of from that are absent from remove.
func subtractKeys(from []string, remove []string) []string {
	var out []string
	for _, key := range from {
		if !containsString(remove, key) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func unionKeys(a []string, b []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, key := range append(append([]string(nil), a...), b...) {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func removeString(values []string, remove string) []string {
	out := values[:0]
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}
