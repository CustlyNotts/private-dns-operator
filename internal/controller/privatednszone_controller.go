package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	"github.com/custlynotts/private-dns-operator/internal/coredns"
	dnsrender "github.com/custlynotts/private-dns-operator/internal/dns"
)

const privateDNSZoneFinalizer = "dns.custlynotts.io/private-zone-cleanup"

type PrivateDNSZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	CoreDNSNamespace  string
	CoreDNSConfigMap  string
	CoreDNSDeployment string
}

func (r *PrivateDNSZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var zones dnsv1alpha1.PrivateDNSZoneList
	if err := r.List(ctx, &zones); err != nil {
		return ctrl.Result{}, err
	}

	var records dnsv1alpha1.PrivateDNSRecordList
	if err := r.List(ctx, &records); err != nil {
		return ctrl.Result{}, err
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.CoreDNSNamespace, Name: r.CoreDNSConfigMap}, &cm); err != nil {
		return ctrl.Result{}, err
	}
	corefile := cm.Data["Corefile"]
	if corefile == "" {
		return ctrl.Result{}, fmt.Errorf("CoreDNS ConfigMap %s/%s does not contain Corefile", r.CoreDNSNamespace, r.CoreDNSConfigMap)
	}

	zoneFiles := map[string]string{}
	var directives []coredns.ZoneDirective
	var zoneKeys []string
	statusUpdates := map[string]dnsv1alpha1.PrivateDNSZoneStatus{}

	for i := range zones.Items {
		zone := &zones.Items[i]
		if !zone.DeletionTimestamp.IsZero() {
			continue
		}
		if !containsString(zone.Finalizers, privateDNSZoneFinalizer) {
			zone.Finalizers = append(zone.Finalizers, privateDNSZoneFinalizer)
			if err := r.Update(ctx, zone); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}

		normalizedZone, err := dnsrender.NormalizeZone(zone.Spec.Zone)
		if err != nil {
			statusUpdates[zone.Name] = failedStatus(*zone, "InvalidZone", err.Error())
			continue
		}
		compiledRecords, validationErrs := r.recordsForZone(*zone, normalizedZone, records.Items)
		if len(validationErrs) > 0 {
			statusUpdates[zone.Name] = failedStatus(*zone, "InvalidRecordSet", validationErrs[0].Error())
			key := dnsrender.ZoneFileKey(normalizedZone)
			if existing, ok := cm.Data[key]; ok {
				zoneFiles[key] = existing
				directives = append(directives, coredns.ZoneDirective{Zone: normalizedZone, DBFile: key, Policy: effectivePolicy(zone.Spec.UnresolvedRecordPolicy), Records: compiledRecords})
				zoneKeys = append(zoneKeys, key)
				status := statusUpdates[zone.Name]
				status.Conditions = append(status.Conditions, condition("LastKnownGoodApplied", metav1.ConditionTrue, "InvalidRecordSet", "kept the previous rendered zone file", zone.Generation))
				statusUpdates[zone.Name] = status
			}
			continue
		}

		zoneFile, err := dnsrender.RenderZone(*zone, compiledRecords, time.Now())
		if err != nil {
			statusUpdates[zone.Name] = failedStatus(*zone, "ZoneRenderFailed", err.Error())
			continue
		}

		policy := effectivePolicy(zone.Spec.UnresolvedRecordPolicy)
		directive := coredns.ZoneDirective{Zone: normalizedZone, DBFile: zoneFile.Key, Policy: policy, Records: compiledRecords}
		directives = append(directives, directive)
		status := dnsv1alpha1.PrivateDNSZoneStatus{
			ObservedGeneration: zone.Generation,
			Conditions:         []metav1.Condition{},
			Records:            renderedRecordCount(compiledRecords),
			Serial:             zoneFile.Serial,
			LastAppliedHash:    zoneFile.Hash,
		}
		if policy == dnsv1alpha1.UnresolvedRecordPolicyForward {
			status.Conditions = append(status.Conditions, condition("TemplateRendered", metav1.ConditionTrue, "Rendered", "CoreDNS template records rendered successfully", zone.Generation))
		} else {
			zoneFiles[zoneFile.Key] = zoneFile.Content
			zoneKeys = append(zoneKeys, zoneFile.Key)
			status.ZoneFileKey = zoneFile.Key
			status.Conditions = append(status.Conditions, condition("ZoneFileRendered", metav1.ConditionTrue, "Rendered", "zone file rendered successfully", zone.Generation))
		}
		statusUpdates[zone.Name] = status
	}

	if hasUnrenderedStatus(statusUpdates) {
		for i := range zones.Items {
			zone := &zones.Items[i]
			status, ok := statusUpdates[zone.Name]
			if !ok || zone.DeletionTimestamp != nil {
				continue
			}
			if renderedStatus(status) {
				continue
			}
			zone.Status = status
			if err := r.Status().Update(ctx, zone); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	patchedCorefile, err := coredns.PatchCorefile(corefile, directives)
	if err != nil {
		return ctrl.Result{}, err
	}
	coredns.ApplyConfigMap(&cm, patchedCorefile, zoneFiles)
	if err := r.Update(ctx, &cm); err != nil {
		return ctrl.Result{}, err
	}

	var dep appsv1.Deployment
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.CoreDNSNamespace, Name: r.CoreDNSDeployment}, &dep); err != nil {
		return ctrl.Result{}, err
	}
	volumeChanged, err := coredns.EnsureZoneVolumeItems(&dep, r.CoreDNSConfigMap, zoneKeys)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !coredns.HasReloadPlugin(patchedCorefile) || volumeChanged {
		coredns.MarkRolloutRestart(&dep, "private-dns-zone-change", combinedHash(statusUpdates))
	}
	if volumeChanged || !coredns.HasReloadPlugin(patchedCorefile) {
		if err := r.Update(ctx, &dep); err != nil {
			return ctrl.Result{}, err
		}
	}

	for i := range zones.Items {
		zone := &zones.Items[i]
		if zone.DeletionTimestamp.IsZero() || !containsString(zone.Finalizers, privateDNSZoneFinalizer) {
			continue
		}
		zone.Finalizers = removeString(zone.Finalizers, privateDNSZoneFinalizer)
		if err := r.Update(ctx, zone); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	for i := range zones.Items {
		zone := &zones.Items[i]
		status, ok := statusUpdates[zone.Name]
		if !ok || !zone.DeletionTimestamp.IsZero() {
			continue
		}
		if !renderedStatus(status) {
			zone.Status = status
			if err := r.Status().Update(ctx, zone); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			continue
		}
		status.Conditions = append(status.Conditions,
			condition("CorefilePatched", metav1.ConditionTrue, "Patched", "CoreDNS Corefile managed block patched", zone.Generation),
			condition("VolumeMounted", metav1.ConditionTrue, "Mounted", "CoreDNS ConfigMap volume items are up to date", zone.Generation),
			condition("ReloadTriggered", metav1.ConditionTrue, reloadReason(patchedCorefile), reloadMessage(patchedCorefile), zone.Generation),
			condition("Ready", metav1.ConditionTrue, "Ready", "private DNS zone is ready", zone.Generation),
		)
		zone.Status = status
		if err := r.Status().Update(ctx, zone); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *PrivateDNSZoneReconciler) recordsForZone(zone dnsv1alpha1.PrivateDNSZone, normalizedZone string, external []dnsv1alpha1.PrivateDNSRecord) ([]dnsrender.Record, []error) {
	var out []dnsrender.Record
	var errs []error
	zoneTTL := dnsrender.DefaultTTL
	if zone.Spec.TTL != nil {
		zoneTTL = *zone.Spec.TTL
	}

	for _, record := range zone.Spec.Records {
		ttl := zoneTTL
		if record.TTL != nil {
			ttl = *record.TTL
		}
		fqdn, err := dnsrender.FQDN(record.Name, normalizedZone)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, dnsrender.Record{
			Zone: normalizedZone, Name: record.Name, FQDN: fqdn, Type: record.Type, TTL: ttl, Values: record.Values,
			Source: dnsrender.Source{Kind: "PrivateDNSZone", Name: zone.Name},
		})
	}

	for _, record := range external {
		if record.Spec.ZoneRef.Name != zone.Name {
			continue
		}
		if !namespaceAllowed(zone.Spec.AllowedNamespaces, record.Namespace) {
			errs = append(errs, fmt.Errorf("%s/%s is not allowed to publish records in zone %s", record.Namespace, record.Name, zone.Name))
			continue
		}
		ttl := zoneTTL
		if record.Spec.TTL != nil {
			ttl = *record.Spec.TTL
		}
		fqdn, err := dnsrender.FQDN(record.Spec.Name, normalizedZone)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, dnsrender.Record{
			Zone: normalizedZone, Name: record.Spec.Name, FQDN: fqdn, Type: record.Spec.Type, TTL: ttl, Values: record.Spec.Values,
			Source: dnsrender.Source{Kind: "PrivateDNSRecord", Namespace: record.Namespace, Name: record.Name},
		})
	}
	errs = append(errs, dnsrender.ValidateRecords(out)...)
	return out, errs
}

func (r *PrivateDNSZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
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

func failedStatus(zone dnsv1alpha1.PrivateDNSZone, reason string, message string) dnsv1alpha1.PrivateDNSZoneStatus {
	return dnsv1alpha1.PrivateDNSZoneStatus{
		ObservedGeneration: zone.Generation,
		Conditions: []metav1.Condition{
			condition("Ready", metav1.ConditionFalse, reason, message, zone.Generation),
		},
		ZoneFileKey: dnsrender.ZoneFileKey(zone.Spec.Zone),
	}
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
	for _, allowed := range selector.MatchNames {
		if allowed == namespace {
			return true
		}
	}
	return false
}

func renderedRecordCount(records []dnsrender.Record) int {
	count := 0
	for _, record := range records {
		count += len(record.Values)
	}
	return count
}

func reloadReason(corefile string) string {
	if coredns.HasReloadPlugin(corefile) {
		return "ReloadPlugin"
	}
	return "RolloutRestart"
}

func reloadMessage(corefile string) string {
	if coredns.HasReloadPlugin(corefile) {
		return "CoreDNS reload plugin will pick up the change"
	}
	return "CoreDNS deployment rollout restart was triggered because reload plugin is missing"
}

func combinedHash(statuses map[string]dnsv1alpha1.PrivateDNSZoneStatus) string {
	var hashes []string
	for _, status := range statuses {
		hashes = append(hashes, status.LastAppliedHash)
	}
	sort.Strings(hashes)
	return strings.Join(hashes, ",")
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

func hasUnrenderedStatus(statuses map[string]dnsv1alpha1.PrivateDNSZoneStatus) bool {
	for _, status := range statuses {
		if !renderedStatus(status) {
			return true
		}
	}
	return false
}

func renderedStatus(status dnsv1alpha1.PrivateDNSZoneStatus) bool {
	return zoneStatusHasCondition(status, "ZoneFileRendered", metav1.ConditionTrue) || zoneStatusHasCondition(status, "TemplateRendered", metav1.ConditionTrue)
}

func zoneStatusHasCondition(status dnsv1alpha1.PrivateDNSZoneStatus, conditionType string, conditionStatus metav1.ConditionStatus) bool {
	for _, cond := range status.Conditions {
		if cond.Type == conditionType && cond.Status == conditionStatus {
			return true
		}
	}
	return false
}
