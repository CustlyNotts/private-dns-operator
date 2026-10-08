package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	"github.com/custlynotts/private-dns-operator/internal/coredns"
)

const baseCorefile = `.:53 {
    errors
    health
    kubernetes cluster.local in-addr.arpa ip6.arpa {
       pods insecure
    }
    forward . /etc/resolv.conf
    cache 30
    reload
}`

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("add client-go scheme: %v", err)
	}
	if err := dnsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add dns scheme: %v", err)
	}
	return scheme
}

func corednsConfigMap(data map[string]string, managedKeys string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
		Data:       data,
	}
	if managedKeys != "" {
		cm.Annotations = map[string]string{coredns.ManagedZoneKeysAnnotation: managedKeys}
	}
	return cm
}

func corednsDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name: "config-volume",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "coredns"},
								Items:                []corev1.KeyToPath{{Key: "Corefile", Path: "Corefile"}},
							},
						},
					}},
				},
			},
		},
	}
}

// zone builds a PrivateDNSZone that already carries the finalizer, so a single
// Reconcile call renders instead of spending the first pass on the finalizer.
func zone(name, suffix string, policy dnsv1alpha1.UnresolvedRecordPolicy, allowed ...string) *dnsv1alpha1.PrivateDNSZone {
	return &dnsv1alpha1.PrivateDNSZone{
		ObjectMeta: metav1.ObjectMeta{Name: name, Finalizers: []string{privateDNSZoneFinalizer}},
		Spec: dnsv1alpha1.PrivateDNSZoneSpec{
			Zone:                   suffix,
			UnresolvedRecordPolicy: policy,
			AllowedNamespaces:      dnsv1alpha1.NamespaceSelector{MatchNames: allowed},
		},
	}
}

func record(name, namespace, zoneRef, recordName string, recordType dnsv1alpha1.DNSRecordType, values ...string) *dnsv1alpha1.PrivateDNSRecord {
	return &dnsv1alpha1.PrivateDNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: dnsv1alpha1.PrivateDNSRecordSpec{
			ZoneRef: dnsv1alpha1.ZoneReference{Name: zoneRef},
			Name:    recordName,
			Type:    recordType,
			Values:  values,
		},
	}
}

func newReconciler(t *testing.T, objects ...client.Object) (*PrivateDNSZoneReconciler, client.Client) {
	t.Helper()
	scheme := testScheme(t)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&dnsv1alpha1.PrivateDNSZone{}, &dnsv1alpha1.PrivateDNSRecord{}).
		Build()
	return &PrivateDNSZoneReconciler{
		Client:            fakeClient,
		Scheme:            scheme,
		CoreDNSNamespace:  "kube-system",
		CoreDNSConfigMap:  "coredns",
		CoreDNSDeployment: "coredns",
	}, fakeClient
}

func runReconcile(t *testing.T, r *PrivateDNSZoneReconciler, name string) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
	if err != nil {
		t.Fatalf("reconcile %q: %v", name, err)
	}
	return result
}

func getCorefile(t *testing.T, c client.Client) string {
	t.Helper()
	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get coredns ConfigMap: %v", err)
	}
	return cm.Data["Corefile"]
}

func getZone(t *testing.T, c client.Client, name string) *dnsv1alpha1.PrivateDNSZone {
	t.Helper()
	var z dnsv1alpha1.PrivateDNSZone
	if err := c.Get(context.Background(), types.NamespacedName{Name: name}, &z); err != nil {
		t.Fatalf("get zone %q: %v", name, err)
	}
	return &z
}

func getRecord(t *testing.T, c client.Client, namespace, name string) *dnsv1alpha1.PrivateDNSRecord {
	t.Helper()
	var rec dnsv1alpha1.PrivateDNSRecord
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, &rec); err != nil {
		t.Fatalf("get record %s/%s: %v", namespace, name, err)
	}
	return &rec
}

func assertCondition(t *testing.T, conditions []metav1.Condition, conditionType string, want metav1.ConditionStatus, wantReason string) {
	t.Helper()
	cond := apimeta.FindStatusCondition(conditions, conditionType)
	if cond == nil {
		t.Fatalf("condition %q is missing; have %v", conditionType, conditionTypes(conditions))
	}
	if cond.Status != want {
		t.Errorf("condition %q status = %v, want %v (reason %q)", conditionType, cond.Status, want, cond.Reason)
	}
	if wantReason != "" && cond.Reason != wantReason {
		t.Errorf("condition %q reason = %q, want %q", conditionType, cond.Reason, wantReason)
	}
}

func conditionTypes(conditions []metav1.Condition) []string {
	var names []string
	for _, cond := range conditions {
		names = append(names, cond.Type)
	}
	return names
}

func TestReconcileClaimsZoneAndRendersInOnePass(t *testing.T) {
	z := zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward)
	z.Finalizers = nil
	r, c := newReconciler(t, z, corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""), corednsDeployment())

	runReconcile(t, r, "alpha")

	stored := getZone(t, c, "alpha")
	if !containsString(stored.Finalizers, privateDNSZoneFinalizer) {
		t.Fatalf("finalizer was not added, have %v", stored.Finalizers)
	}
	// Claiming the zone and rendering it happen in the same reconcile, so the
	// status write has to survive the resourceVersion bump from the claim.
	if !strings.Contains(getCorefile(t, c), "alpha.internal:53 {") {
		t.Error("the zone should be rendered in the same pass that claims it")
	}
	assertCondition(t, stored.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
}

func TestReconcileRendersForwardZoneAndRecordStatus(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1", "10.0.0.2"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	corefile := getCorefile(t, c)
	for _, want := range []string{
		coredns.ManagedBlockStart,
		"alpha.internal:53 {",
		"template IN A alpha.internal {",
		`answer "{{ .Name }} 300 IN A 10.0.0.1"`,
		`answer "{{ .Name }} 300 IN A 10.0.0.2"`,
		"fallthrough",
		"forward . /etc/resolv.conf",
	} {
		if !strings.Contains(corefile, want) {
			t.Errorf("Corefile missing %q:\n%s", want, corefile)
		}
	}

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionTemplateRendered, metav1.ConditionTrue, reasonRendered)
	assertCondition(t, z.Status.Conditions, ConditionCorefilePatched, metav1.ConditionTrue, "")
	assertCondition(t, z.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	if z.Status.Records != 2 {
		t.Errorf("status.records = %d, want 2", z.Status.Records)
	}
	// Forward mode renders into the Corefile, not a zone file.
	if z.Status.ZoneFileKey != "" {
		t.Errorf("Forward mode should not report a zone file key, got %q", z.Status.ZoneFileKey)
	}

	rec := getRecord(t, c, "default", "alpha-api")
	if rec.Status.FQDN != "api.alpha.internal" {
		t.Errorf("record status.fqdn = %q, want %q", rec.Status.FQDN, "api.alpha.internal")
	}
	assertCondition(t, rec.Status.Conditions, ConditionAccepted, metav1.ConditionTrue, reasonAccepted)
}

func TestReconcileRendersNXDOMAINZoneFile(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionZoneFileRendered, metav1.ConditionTrue, reasonRendered)
	if z.Status.ZoneFileKey == "" {
		t.Fatal("NXDOMAIN mode should report the rendered zone file key")
	}

	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if !strings.Contains(cm.Data[z.Status.ZoneFileKey], "api 300 IN A 10.0.0.1") {
		t.Errorf("zone file content:\n%s", cm.Data[z.Status.ZoneFileKey])
	}
	if !strings.Contains(cm.Data["Corefile"], "file /etc/coredns/"+z.Status.ZoneFileKey+" alpha.internal") {
		t.Errorf("Corefile should load the zone file:\n%s", cm.Data["Corefile"])
	}

	// The zone file has to be mounted for CoreDNS to read it.
	var dep appsv1.Deployment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	var mounted bool
	for _, item := range dep.Spec.Template.Spec.Volumes[0].ConfigMap.Items {
		if item.Key == z.Status.ZoneFileKey {
			mounted = true
		}
	}
	if !mounted {
		t.Error("the rendered zone file was not added to the CoreDNS volume items")
	}
}

// TestReconcileInvalidZoneDoesNotBlockHealthyZones is the regression test for a
// single broken zone preventing every other zone from reaching CoreDNS.
func TestReconcileInvalidZoneDoesNotBlockHealthyZones(t *testing.T) {
	r, c := newReconciler(t,
		zone("healthy", "healthy.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		zone("broken", "not a valid zone!", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("healthy-api", "default", "healthy", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "broken")

	corefile := getCorefile(t, c)
	if !strings.Contains(corefile, "healthy.internal:53 {") {
		t.Fatalf("the healthy zone must still be rendered when another zone is invalid:\n%s", corefile)
	}

	assertCondition(t, getZone(t, c, "healthy").Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	assertCondition(t, getZone(t, c, "broken").Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonInvalidZone)
	assertCondition(t, getRecord(t, c, "default", "healthy-api").Status.Conditions, ConditionAccepted, metav1.ConditionTrue, reasonAccepted)
}

// TestReconcileInvalidRecordSetDoesNotBlockHealthyZones covers the same
// isolation for the more common failure: a valid zone whose records conflict.
func TestReconcileInvalidRecordSetDoesNotBlockHealthyZones(t *testing.T) {
	r, c := newReconciler(t,
		zone("healthy", "healthy.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		zone("conflicted", "conflicted.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("healthy-api", "default", "healthy", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("conflict-a", "default", "conflicted", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("conflict-cname", "default", "conflicted", "api", dnsv1alpha1.DNSRecordTypeCNAME, "elsewhere.example.com"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "conflicted")

	if corefile := getCorefile(t, c); !strings.Contains(corefile, "healthy.internal:53 {") {
		t.Fatalf("the healthy zone must still be rendered:\n%s", corefile)
	}
	assertCondition(t, getZone(t, c, "healthy").Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	assertCondition(t, getZone(t, c, "conflicted").Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonInvalidRecordSet)
}

func TestReconcileHoldsLastKnownGoodOutput(t *testing.T) {
	objects := []client.Object{
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	}
	r, c := newReconciler(t, objects...)
	runReconcile(t, r, "alpha")

	healthyCorefile := getCorefile(t, c)
	zoneFileKey := getZone(t, c, "alpha").Status.ZoneFileKey
	if !strings.Contains(healthyCorefile, "alpha.internal:53 {") {
		t.Fatalf("setup did not render the zone:\n%s", healthyCorefile)
	}

	// Introduce a CNAME that conflicts with the existing A record.
	if err := c.Create(context.Background(), record("alpha-bad", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeCNAME, "elsewhere.example.com")); err != nil {
		t.Fatalf("create conflicting record: %v", err)
	}
	runReconcile(t, r, "alpha")

	corefile := getCorefile(t, c)
	if !strings.Contains(corefile, "alpha.internal:53 {") {
		t.Fatalf("an invalid edit must not take the zone out of DNS:\n%s", corefile)
	}
	if !strings.Contains(corefile, "file /etc/coredns/"+zoneFileKey+" alpha.internal") {
		t.Errorf("the previously rendered stanza was not carried forward:\n%s", corefile)
	}

	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if !strings.Contains(cm.Data[zoneFileKey], "api 300 IN A 10.0.0.1") {
		t.Errorf("the last known good zone file was dropped: %q", cm.Data[zoneFileKey])
	}

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonInvalidRecordSet)
	assertCondition(t, z.Status.Conditions, ConditionLastKnownGoodApplied, metav1.ConditionTrue, reasonInvalidRecordSet)
}

func TestReconcileDropsZoneConditionsOnRecovery(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	bad := record("alpha-bad", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeCNAME, "elsewhere.example.com")
	if err := c.Create(context.Background(), bad); err != nil {
		t.Fatalf("create conflicting record: %v", err)
	}
	runReconcile(t, r, "alpha")
	if apimeta.FindStatusCondition(getZone(t, c, "alpha").Status.Conditions, ConditionLastKnownGoodApplied) == nil {
		t.Fatal("setup did not reach the held state")
	}

	if err := c.Delete(context.Background(), bad); err != nil {
		t.Fatalf("delete conflicting record: %v", err)
	}
	runReconcile(t, r, "alpha")

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	if cond := apimeta.FindStatusCondition(z.Status.Conditions, ConditionLastKnownGoodApplied); cond != nil {
		t.Errorf("LastKnownGoodApplied should be pruned once the zone recovers, got %+v", cond)
	}
}

func TestReconcileRejectsRecordFromDisallowedNamespace(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward, "platform"),
		record("intruder", "tenant-b", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.9"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	rec := getRecord(t, c, "tenant-b", "intruder")
	assertCondition(t, rec.Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonNamespaceNotAllowed)
	if rec.Status.FQDN != "" {
		t.Errorf("a rejected record should not report an FQDN, got %q", rec.Status.FQDN)
	}
	if corefile := getCorefile(t, c); strings.Contains(corefile, "10.0.0.9") {
		t.Errorf("a disallowed record must not be published:\n%s", corefile)
	}
}

func TestReconcileReportsZoneNotFoundOnOrphanRecord(t *testing.T) {
	r, c := newReconciler(t,
		record("orphan", "default", "missing", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "missing")

	assertCondition(t, getRecord(t, c, "default", "orphan").Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonZoneNotFound)
}

func TestReconcileReportsInvalidRecordValue(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("bad-ip", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "not-an-ip"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	rec := getRecord(t, c, "default", "bad-ip")
	assertCondition(t, rec.Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonInvalidRecord)
	if !strings.Contains(apimeta.FindStatusCondition(rec.Status.Conditions, ConditionAccepted).Message, "IPv4") {
		t.Errorf("the record should explain why it was rejected, got %q",
			apimeta.FindStatusCondition(rec.Status.Conditions, ConditionAccepted).Message)
	}
}

// TestReconcileIsIdempotent guards against the status write loop: a second pass
// over unchanged input must not bump any resourceVersion, because every status
// write re-triggers the watch that scheduled the reconcile.
func TestReconcileIsIdempotent(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	zoneVersion := getZone(t, c, "alpha").ResourceVersion
	recordVersion := getRecord(t, c, "default", "alpha-api").ResourceVersion
	corefile := getCorefile(t, c)

	runReconcile(t, r, "alpha")

	if got := getZone(t, c, "alpha").ResourceVersion; got != zoneVersion {
		t.Errorf("zone status was rewritten on an unchanged reconcile (%s -> %s)", zoneVersion, got)
	}
	if got := getRecord(t, c, "default", "alpha-api").ResourceVersion; got != recordVersion {
		t.Errorf("record status was rewritten on an unchanged reconcile (%s -> %s)", recordVersion, got)
	}
	if got := getCorefile(t, c); got != corefile {
		t.Errorf("Corefile changed on an unchanged reconcile:\n%s", got)
	}
}

func TestReconcileRemovesZoneOutputOnDeletion(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		zone("beta", "beta.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("beta-api", "default", "beta", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.1.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	alphaKey := getZone(t, c, "alpha").Status.ZoneFileKey

	if err := c.Delete(context.Background(), getZone(t, c, "alpha")); err != nil {
		t.Fatalf("delete zone: %v", err)
	}
	runReconcile(t, r, "alpha")

	corefile := getCorefile(t, c)
	if strings.Contains(corefile, "alpha.internal:53 {") {
		t.Errorf("the deleted zone's stanza should be gone:\n%s", corefile)
	}
	if !strings.Contains(corefile, "beta.internal:53 {") {
		t.Errorf("the surviving zone should be untouched:\n%s", corefile)
	}

	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if _, ok := cm.Data[alphaKey]; ok {
		t.Error("the deleted zone's zone file should be removed from the ConfigMap")
	}

	var remaining dnsv1alpha1.PrivateDNSZone
	err := c.Get(context.Background(), types.NamespacedName{Name: "alpha"}, &remaining)
	if err == nil {
		t.Errorf("the finalizer should have been released, zone still present with finalizers %v", remaining.Finalizers)
	}
}

func TestReconcileRemovesDeletedRecordFromDNS(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("alpha-one", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("alpha-two", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.2"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	if corefile := getCorefile(t, c); !strings.Contains(corefile, "10.0.0.2") {
		t.Fatalf("setup did not render both records:\n%s", corefile)
	}

	if err := c.Delete(context.Background(), getRecord(t, c, "default", "alpha-two")); err != nil {
		t.Fatalf("delete record: %v", err)
	}
	runReconcile(t, r, "alpha")

	corefile := getCorefile(t, c)
	if strings.Contains(corefile, "10.0.0.2") {
		t.Errorf("the deleted record's answer should be gone:\n%s", corefile)
	}
	if !strings.Contains(corefile, "10.0.0.1") {
		t.Errorf("the surviving record should still be published:\n%s", corefile)
	}
}

func TestReconcileTriggersRolloutRestartWithoutReloadPlugin(t *testing.T) {
	noReload := strings.ReplaceAll(baseCorefile, "    reload\n", "")
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": noReload}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	var dep appsv1.Deployment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if dep.Spec.Template.Annotations["dns.custlynotts.io/restarted-for"] == "" {
		t.Error("a Corefile without the reload plugin should trigger a rollout restart")
	}
	assertCondition(t, getZone(t, c, "alpha").Status.Conditions, ConditionReloadTriggered, metav1.ConditionTrue, "RolloutRestart")
}

func TestReconcileErrorsWhenCorefileIsMissing(t *testing.T) {
	r, _ := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		corednsConfigMap(map[string]string{}, ""),
		corednsDeployment(),
	)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha"}})
	if err == nil {
		t.Fatal("expected an error when the ConfigMap has no Corefile key")
	}
	if !strings.Contains(err.Error(), "Corefile") {
		t.Errorf("error should name the missing key, got %v", err)
	}
}

func TestReconcileRendersInlineZoneRecords(t *testing.T) {
	z := zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward)
	z.Spec.Records = []dnsv1alpha1.PrivateDNSRecordSpecInline{{
		Name:   "info",
		Type:   dnsv1alpha1.DNSRecordTypeTXT,
		Values: []string{"owned by platform"},
	}}
	r, c := newReconciler(t, z, corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""), corednsDeployment())
	runReconcile(t, r, "alpha")

	if corefile := getCorefile(t, c); !strings.Contains(corefile, `IN TXT \"owned by platform\"`) {
		t.Errorf("inline zone records should be rendered:\n%s", corefile)
	}
}

func TestNamespaceAllowed(t *testing.T) {
	tests := []struct {
		name     string
		selector dnsv1alpha1.NamespaceSelector
		ns       string
		want     bool
	}{
		{name: "empty selector allows everything", selector: dnsv1alpha1.NamespaceSelector{}, ns: "anything", want: true},
		{name: "listed namespace", selector: dnsv1alpha1.NamespaceSelector{MatchNames: []string{"a", "b"}}, ns: "b", want: true},
		{name: "unlisted namespace", selector: dnsv1alpha1.NamespaceSelector{MatchNames: []string{"a", "b"}}, ns: "c", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := namespaceAllowed(test.selector, test.ns); got != test.want {
				t.Errorf("namespaceAllowed() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestEffectivePolicyDefaultsToNXDOMAIN(t *testing.T) {
	if got := effectivePolicy(""); got != dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN {
		t.Errorf("effectivePolicy(\"\") = %q, want NXDOMAIN", got)
	}
	if got := effectivePolicy(dnsv1alpha1.UnresolvedRecordPolicyForward); got != dnsv1alpha1.UnresolvedRecordPolicyForward {
		t.Errorf("effectivePolicy(Forward) = %q", got)
	}
}

// deleteZone removes a zone and returns it in its terminating state. The fake
// client honours finalizers, so the object survives the Delete call.
func deleteZone(t *testing.T, c client.Client, name string) {
	t.Helper()
	if err := c.Delete(context.Background(), getZone(t, c, name)); err != nil {
		t.Fatalf("delete zone %q: %v", name, err)
	}
	if getZone(t, c, name).DeletionTimestamp.IsZero() {
		t.Fatalf("zone %q should be terminating, held by its finalizer", name)
	}
}

func zoneExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	var z dnsv1alpha1.PrivateDNSZone
	err := c.Get(context.Background(), types.NamespacedName{Name: name}, &z)
	if err == nil {
		return true
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("get zone %q: %v", name, err)
	}
	return false
}

// TestReconcileReleasesFinalizerWhenCoreDNSConfigMapIsMissing covers the
// footgun of pointing the operator at a CoreDNS ConfigMap that does not exist,
// for example a typo in coredns.configMap. Without this, every zone created
// against that target becomes permanently undeletable and blocks CRD deletion
// too, even though the operator is running and healthy.
func TestReconcileReleasesFinalizerWhenCoreDNSConfigMapIsMissing(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		corednsDeployment(),
	)
	deleteZone(t, c, "alpha")

	// The reconcile still fails, because the target really is misconfigured.
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha"}})
	if err == nil {
		t.Fatal("expected an error for a missing CoreDNS ConfigMap")
	}
	if zoneExists(t, c, "alpha") {
		t.Error("a terminating zone must not be held by a finalizer when there is no CoreDNS state to clean up")
	}
}

func TestReconcileReleasesFinalizerWhenCorefileKeyIsMissing(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		corednsConfigMap(map[string]string{"NotACorefile": "x"}, ""),
		corednsDeployment(),
	)
	deleteZone(t, c, "alpha")

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha"}}); err == nil {
		t.Fatal("expected an error for a ConfigMap with no Corefile key")
	}
	if zoneExists(t, c, "alpha") {
		t.Error("a ConfigMap with no Corefile holds no managed block, so the finalizer should be released")
	}
}

// TestReconcileReportsCoreDNSUnavailable checks that a misconfigured target is
// explained on the zone, rather than only showing up in operator logs.
func TestReconcileReportsCoreDNSUnavailable(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsDeployment(),
	)

	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha"}}); err == nil {
		t.Fatal("expected an error for a missing CoreDNS ConfigMap")
	}

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonCoreDNSUnavailable)
	if msg := apimeta.FindStatusCondition(z.Status.Conditions, ConditionReady).Message; !strings.Contains(msg, "kube-system/coredns") {
		t.Errorf("the condition should name the target it could not reach, got %q", msg)
	}
	// A live zone must not be released, since it is not being deleted.
	if !zoneExists(t, c, "alpha") {
		t.Error("a live zone should not be removed")
	}
	assertCondition(t, getRecord(t, c, "default", "alpha-api").Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonZoneNotReady)
}

// TestReconcileKeepsFinalizerWhenCoreDNSCannotBeRead draws the other half of
// the line: when the target cannot be read, the managed block may still be
// live, so the finalizer has to hold rather than orphan a stanza.
func TestReconcileKeepsFinalizerWhenCoreDNSCannotBeRead(t *testing.T) {
	scheme := testScheme(t)
	z := zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward)
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(z, corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""), corednsDeployment()).
		WithStatusSubresource(&dnsv1alpha1.PrivateDNSZone{}, &dnsv1alpha1.PrivateDNSRecord{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.ConfigMap); ok {
					return apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, key.Name, errors.New("not permitted"))
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
	r := &PrivateDNSZoneReconciler{
		Client:            fakeClient,
		Scheme:            scheme,
		CoreDNSNamespace:  "kube-system",
		CoreDNSConfigMap:  "coredns",
		CoreDNSDeployment: "coredns",
	}

	deleteZone(t, r.Client, "alpha")
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: "alpha"}}); err == nil {
		t.Fatal("expected the Forbidden error to surface")
	}
	if !zoneExists(t, r.Client, "alpha") {
		t.Error("an unreadable target may still hold a managed block, so the finalizer must not be released")
	}
}

// TestDisallowedNamespaceCannotFreezeTheZone is the regression test for a
// denial-of-service against the delegation boundary. A PrivateDNSRecord in a
// namespace that allowedNamespaces explicitly excludes used to be reported as an
// invalid record set, which blocked the whole zone: Ready went False, the record
// count was cleared, and no further record from an *allowed* namespace could be
// published until the offending object was deleted.
func TestDisallowedNamespaceCannotFreezeTheZone(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward, "default"),
		record("allowed", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("rogue", "tenant-x", "alpha", "rogue", dnsv1alpha1.DNSRecordTypeA, "10.6.6.6"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	z := getZone(t, c, "alpha")
	assertCondition(t, z.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	if z.Status.Records != 1 {
		t.Errorf("zone should still publish its one allowed record, status.records = %d", z.Status.Records)
	}

	// The allowed record keeps working; only the barred one is rejected.
	assertCondition(t, getRecord(t, c, "default", "allowed").Status.Conditions, ConditionAccepted, metav1.ConditionTrue, reasonAccepted)
	assertCondition(t, getRecord(t, c, "tenant-x", "rogue").Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonNamespaceNotAllowed)

	corefile := getCorefile(t, c)
	if !strings.Contains(corefile, "10.0.0.1") {
		t.Errorf("the allowed record must be published:\n%s", corefile)
	}
	if strings.Contains(corefile, "10.6.6.6") {
		t.Errorf("the barred record must not be published:\n%s", corefile)
	}

	// And a new record from an allowed namespace must still get through while the
	// barred object still exists.
	if err := c.Create(context.Background(), record("later", "default", "alpha", "web", dnsv1alpha1.DNSRecordTypeA, "10.1.1.1")); err != nil {
		t.Fatalf("create later record: %v", err)
	}
	runReconcile(t, r, "alpha")
	if !strings.Contains(getCorefile(t, c), "10.1.1.1") {
		t.Error("a later record from an allowed namespace must publish even while a barred record exists")
	}
	assertCondition(t, getRecord(t, c, "default", "later").Status.Conditions, ConditionAccepted, metav1.ConditionTrue, reasonAccepted)
}

// TestInvalidDelegatedRecordCannotFreezeTheZone covers the same containment for a
// malformed tenant record rather than a policy violation.
func TestInvalidDelegatedRecordCannotFreezeTheZone(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("good", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("bad", "default", "alpha", "broken", dnsv1alpha1.DNSRecordTypeA, "not-an-ip"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	assertCondition(t, getZone(t, c, "alpha").Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready")
	assertCondition(t, getRecord(t, c, "default", "good").Status.Conditions, ConditionAccepted, metav1.ConditionTrue, reasonAccepted)
	assertCondition(t, getRecord(t, c, "default", "bad").Status.Conditions, ConditionAccepted, metav1.ConditionFalse, reasonInvalidRecord)
	if !strings.Contains(getCorefile(t, c), "10.0.0.1") {
		t.Error("the valid record must still be published")
	}
}

// TestInvalidInlineZoneRecordBlocksTheZone draws the other half of the line. An
// inline record is part of the zone's own spec, so the zone author breaking it
// makes the zone's desired state invalid and the previous output is held.
func TestInvalidInlineZoneRecordBlocksTheZone(t *testing.T) {
	z := zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward)
	z.Spec.Records = []dnsv1alpha1.PrivateDNSRecordSpecInline{{
		Name:   "broken",
		Type:   dnsv1alpha1.DNSRecordTypeA,
		Values: []string{"not-an-ip"},
	}}
	r, c := newReconciler(t, z,
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")

	assertCondition(t, getZone(t, c, "alpha").Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonInvalidRecordSet)
}

// TestCNAMEConflictBetweenAdmittedRecordsBlocksTheZone confirms a genuine
// set-level inconsistency still fails safe. Both records here are from permitted
// namespaces, so this is a correctness problem between authorised publishers
// rather than a privilege violation.
func TestCNAMEConflictBetweenAdmittedRecordsBlocksTheZone(t *testing.T) {
	r, c := newReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("a-record", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		record("cname", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeCNAME, "elsewhere.example.com"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	assertCondition(t, getZone(t, c, "alpha").Status.Conditions, ConditionReady, metav1.ConditionFalse, reasonInvalidRecordSet)
}
