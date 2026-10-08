package controller

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	"github.com/custlynotts/private-dns-operator/internal/coredns"
)

// corefileFileRef matches the zone file a `file` plugin directive loads.
var corefileFileRef = regexp.MustCompile(`(?m)^\s*file\s+/etc/coredns/(\S+)\s`)

// coreDNSWitness records the CoreDNS ConfigMap and Deployment after every write
// the operator makes, and checks the two invariants a CoreDNS ConfigMap mount
// imposes on each resulting state:
//
//   - every key named in the Deployment's volume items must exist in the
//     ConfigMap, or the volume fails to populate and new CoreDNS pods are stuck
//     in FailedMount
//   - every file named by the Corefile must be mounted in the container, or
//     CoreDNS exits during startup and crash-loops
//
// Writing the ConfigMap and the Deployment is never atomic, so the operator has
// to sequence its writes such that both hold after each one individually.
type coreDNSWitness struct {
	t          *testing.T
	cm         *corev1.ConfigMap
	dep        *appsv1.Deployment
	violations []string
	writes     int
}

func (w *coreDNSWitness) observe(obj client.Object) {
	switch typed := obj.(type) {
	case *corev1.ConfigMap:
		if typed.Name != "coredns" {
			return
		}
		w.cm = typed.DeepCopy()
		w.writes++
	case *appsv1.Deployment:
		if typed.Name != "coredns" {
			return
		}
		w.dep = typed.DeepCopy()
		w.writes++
	default:
		return
	}
	w.check()
}

func (w *coreDNSWitness) check() {
	if w.cm == nil || w.dep == nil {
		return
	}

	mounted := map[string]struct{}{}
	for _, volume := range w.dep.Spec.Template.Spec.Volumes {
		if volume.ConfigMap == nil || volume.ConfigMap.Name != "coredns" {
			continue
		}
		for _, item := range volume.ConfigMap.Items {
			mounted[item.Key] = struct{}{}
			if _, ok := w.cm.Data[item.Key]; !ok {
				w.violations = append(w.violations, fmt.Sprintf(
					"after write %d: volume item %q has no matching ConfigMap key, CoreDNS pods would fail to mount",
					w.writes, item.Key))
			}
		}
	}

	// A volume with no explicit items projects every key, so the Corefile can
	// reference anything that exists.
	explicitItems := len(mounted) > 0
	for _, match := range corefileFileRef.FindAllStringSubmatch(w.cm.Data["Corefile"], -1) {
		key := match[1]
		if _, ok := w.cm.Data[key]; !ok {
			w.violations = append(w.violations, fmt.Sprintf(
				"after write %d: Corefile loads %q which is not a ConfigMap key", w.writes, key))
			continue
		}
		if explicitItems {
			if _, ok := mounted[key]; !ok {
				w.violations = append(w.violations, fmt.Sprintf(
					"after write %d: Corefile loads %q which is not mounted, CoreDNS would crash on startup",
					w.writes, key))
			}
		}
	}
}

func newWitnessedReconciler(t *testing.T, objects ...client.Object) (*PrivateDNSZoneReconciler, client.Client, *coreDNSWitness) {
	t.Helper()
	scheme := testScheme(t)
	witness := &coreDNSWitness{t: t}
	for _, obj := range objects {
		switch typed := obj.(type) {
		case *corev1.ConfigMap:
			witness.cm = typed.DeepCopy()
		case *appsv1.Deployment:
			witness.dep = typed.DeepCopy()
		}
	}

	record := func(obj client.Object) { witness.observe(obj) }
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&dnsv1alpha1.PrivateDNSZone{}, &dnsv1alpha1.PrivateDNSRecord{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if err := c.Update(ctx, obj, opts...); err != nil {
					return err
				}
				record(obj)
				return nil
			},
		}).
		Build()

	return &PrivateDNSZoneReconciler{
		Client:            fakeClient,
		Scheme:            scheme,
		CoreDNSNamespace:  "kube-system",
		CoreDNSConfigMap:  "coredns",
		CoreDNSDeployment: "coredns",
	}, fakeClient, witness
}

func (w *coreDNSWitness) assertClean(t *testing.T, stage string) {
	t.Helper()
	if len(w.violations) > 0 {
		for _, v := range w.violations {
			t.Errorf("%s: %s", stage, v)
		}
	}
}

// TestCoreDNSWritesNeverBreakTheMount is the regression test for the write
// ordering. Adding a zone needs the ConfigMap written before the Deployment;
// removing one needs the opposite. Since the two objects cannot be written
// atomically, a single fixed order is wrong in one of the two directions.
func TestCoreDNSWritesNeverBreakTheMount(t *testing.T) {
	r, c, witness := newWitnessedReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)

	// Create a zone: a new key appears and has to be mounted.
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after creating a zone")
	alphaKey := getZone(t, c, "alpha").Status.ZoneFileKey
	if alphaKey == "" {
		t.Fatal("expected a rendered zone file key")
	}

	// Add a second zone alongside the first.
	if err := c.Create(context.Background(), zone("beta", "beta.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN)); err != nil {
		t.Fatalf("create second zone: %v", err)
	}
	if err := c.Create(context.Background(), record("beta-api", "default", "beta", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.1.1")); err != nil {
		t.Fatalf("create second record: %v", err)
	}
	runReconcile(t, r, "beta")
	witness.assertClean(t, "after adding a second zone")

	// Delete the first zone: its key has to be unmounted before it is deleted.
	if err := c.Delete(context.Background(), getZone(t, c, "alpha")); err != nil {
		t.Fatalf("delete zone: %v", err)
	}
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after deleting a zone")

	// Swap a zone's suffix, which retires one key and introduces another in the
	// same reconcile. This is the case a single fixed write order cannot satisfy.
	beta := getZone(t, c, "beta")
	beta.Spec.Zone = "gamma.internal"
	if err := c.Update(context.Background(), beta); err != nil {
		t.Fatalf("rename zone suffix: %v", err)
	}
	runReconcile(t, r, "beta")
	witness.assertClean(t, "after retiring one key and adding another at once")

	// And confirm the end state is actually correct, not just safely ordered.
	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if _, ok := cm.Data[alphaKey]; ok {
		t.Error("the deleted zone's file should be gone from the ConfigMap")
	}
	managed := coredns.ManagedZoneKeys(&cm)
	if len(managed) != 1 {
		t.Errorf("managed keys = %v, want exactly the surviving zone", managed)
	}
	for _, key := range managed {
		if _, ok := cm.Data[key]; !ok {
			t.Errorf("managed key %q is recorded but missing from the ConfigMap", key)
		}
	}
}

// TestCoreDNSSteadyStateWritesNothing guards the cost of the extra phases: a
// reconcile with nothing to change must not write to CoreDNS at all.
func TestCoreDNSSteadyStateWritesNothing(t *testing.T) {
	r, _, witness := newWitnessedReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after the initial render")

	before := witness.writes
	runReconcile(t, r, "alpha")
	runReconcile(t, r, "alpha")
	if witness.writes != before {
		t.Errorf("a steady-state reconcile wrote to CoreDNS %d times, want 0", witness.writes-before)
	}
}

// TestCoreDNSAddOnlyWriteCount pins the write cost of the common case so the
// extra ordering phases cannot quietly turn into write amplification.
func TestCoreDNSAddOnlyWriteCount(t *testing.T) {
	r, _, witness := newWitnessedReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after creating a zone")

	// ConfigMap (add key + widen annotation), Deployment (mount it), ConfigMap
	// (switch the Corefile). Nothing is retired, so no contract phase writes.
	if witness.writes != 3 {
		t.Errorf("creating a zone took %d CoreDNS writes, want 3", witness.writes)
	}
}

// TestCoreDNSForwardZoneWritesNoZoneFile checks that Forward mode, which renders
// into the Corefile instead of a mounted file, does not touch volume items.
func TestCoreDNSForwardZoneWritesNoZoneFile(t *testing.T) {
	r, c, witness := newWitnessedReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyForward),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after creating a Forward zone")

	var dep appsv1.Deployment
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	items := dep.Spec.Template.Spec.Volumes[0].ConfigMap.Items
	if len(items) != 1 || items[0].Key != "Corefile" {
		t.Errorf("Forward mode should not mount a zone file, items = %v", items)
	}
	if witness.writes != 1 {
		t.Errorf("a Forward zone took %d CoreDNS writes, want 1 for the Corefile", witness.writes)
	}
}

func TestReconcileSwitchingPolicyRetiresTheZoneFile(t *testing.T) {
	r, c, witness := newWitnessedReconciler(t,
		zone("alpha", "alpha.internal", dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN),
		record("alpha-api", "default", "alpha", "api", dnsv1alpha1.DNSRecordTypeA, "10.0.0.1"),
		corednsConfigMap(map[string]string{"Corefile": baseCorefile}, ""),
		corednsDeployment(),
	)
	runReconcile(t, r, "alpha")
	key := getZone(t, c, "alpha").Status.ZoneFileKey

	// Moving to Forward mode means the zone file is no longer needed at all.
	z := getZone(t, c, "alpha")
	z.Spec.UnresolvedRecordPolicy = dnsv1alpha1.UnresolvedRecordPolicyForward
	if err := c.Update(context.Background(), z); err != nil {
		t.Fatalf("switch policy: %v", err)
	}
	runReconcile(t, r, "alpha")
	witness.assertClean(t, "after switching policy")

	var cm corev1.ConfigMap
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "kube-system", Name: "coredns"}, &cm); err != nil {
		t.Fatalf("get ConfigMap: %v", err)
	}
	if _, ok := cm.Data[key]; ok {
		t.Error("the retired zone file should be removed from the ConfigMap")
	}
	if len(coredns.ManagedZoneKeys(&cm)) != 0 {
		t.Errorf("no keys should remain managed, got %v", coredns.ManagedZoneKeys(&cm))
	}
}
