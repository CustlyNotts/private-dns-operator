package v1alpha1_test

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

// TestAddToSchemeRegistersEveryKind guards the scheme registration. The types
// files register themselves through init, so a new types file that forgets to
// call SchemeBuilder.Register compiles and tests fine until something actually
// tries to read that kind from a cluster.
func TestAddToSchemeRegistersEveryKind(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := dnsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	for _, object := range []runtime.Object{
		&dnsv1alpha1.PrivateDNSZone{},
		&dnsv1alpha1.PrivateDNSZoneList{},
		&dnsv1alpha1.PrivateDNSRecord{},
		&dnsv1alpha1.PrivateDNSRecordList{},
	} {
		gvks, _, err := scheme.ObjectKinds(object)
		if err != nil {
			t.Errorf("%T is not registered: %v", object, err)
			continue
		}
		var found bool
		for _, gvk := range gvks {
			if gvk.GroupVersion() == dnsv1alpha1.GroupVersion {
				found = true
			}
		}
		if !found {
			t.Errorf("%T is registered, but not under %s (got %v)", object, dnsv1alpha1.GroupVersion, gvks)
		}
	}

	// controller-runtime's builder added this implicitly; apimachinery's does not,
	// so assert the group version itself is present rather than assuming it.
	if !scheme.IsVersionRegistered(dnsv1alpha1.GroupVersion) {
		t.Errorf("group version %s is not registered with the scheme", dnsv1alpha1.GroupVersion)
	}
}
