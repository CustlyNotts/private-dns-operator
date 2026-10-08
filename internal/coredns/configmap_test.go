package coredns

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testConfigMap(data map[string]string, managedKeys string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{Data: data}
	if managedKeys != "" {
		cm.Annotations = map[string]string{ManagedZoneKeysAnnotation: managedKeys}
	}
	return cm
}

func TestSetZoneFilesOnlyAddsAndUpdates(t *testing.T) {
	cm := testConfigMap(map[string]string{
		"Corefile":        ".:53 {\n}",
		"keep-a1b2c3.db":  "old content",
		"stale-d4e5f6.db": "stale",
		"handwritten.db":  "someone else owns this",
	}, "keep-a1b2c3.db,stale-d4e5f6.db")

	changed := SetZoneFiles(cm, map[string]string{
		"keep-a1b2c3.db":  "new content",
		"fresh-778899.db": "fresh",
	})

	if !changed {
		t.Error("adding and updating keys should report a change")
	}
	if cm.Data["keep-a1b2c3.db"] != "new content" {
		t.Error("an existing zone file should be updated")
	}
	if cm.Data["fresh-778899.db"] != "fresh" {
		t.Error("a new zone file should be added")
	}
	// The whole point of splitting this out: nothing is removed, so the
	// Deployment's volume items can still reference the stale key safely.
	if cm.Data["stale-d4e5f6.db"] != "stale" {
		t.Error("SetZoneFiles must not remove a stale key")
	}
	if cm.Data["handwritten.db"] != "someone else owns this" {
		t.Error("SetZoneFiles must not touch an unmanaged key")
	}
	if cm.Data["Corefile"] != ".:53 {\n}" {
		t.Error("SetZoneFiles must not touch the Corefile")
	}
}

func TestSetZoneFilesReportsNoChangeWhenCurrent(t *testing.T) {
	cm := testConfigMap(map[string]string{"zone-a1b2c3.db": "content"}, "zone-a1b2c3.db")
	if SetZoneFiles(cm, map[string]string{"zone-a1b2c3.db": "content"}) {
		t.Error("rewriting identical content should not report a change, or every reconcile would write")
	}
}

func TestSetCorefile(t *testing.T) {
	cm := testConfigMap(map[string]string{"Corefile": "old"}, "")
	if !SetCorefile(cm, "new") {
		t.Error("changing the Corefile should report a change")
	}
	if cm.Data["Corefile"] != "new" {
		t.Errorf("Corefile = %q", cm.Data["Corefile"])
	}
	if SetCorefile(cm, "new") {
		t.Error("writing an identical Corefile should not report a change")
	}
}

func TestRemoveZoneFiles(t *testing.T) {
	cm := testConfigMap(map[string]string{
		"Corefile":        ".:53 {\n}",
		"stale-a1b2c3.db": "stale",
		"keep-d4e5f6.db":  "keep",
	}, "stale-a1b2c3.db,keep-d4e5f6.db")

	if !RemoveZoneFiles(cm, []string{"stale-a1b2c3.db"}) {
		t.Error("removing a present key should report a change")
	}
	if _, ok := cm.Data["stale-a1b2c3.db"]; ok {
		t.Error("the stale key should be gone")
	}
	if cm.Data["keep-d4e5f6.db"] != "keep" {
		t.Error("an unnamed key should survive")
	}
	if RemoveZoneFiles(cm, []string{"stale-a1b2c3.db"}) {
		t.Error("removing an absent key should not report a change")
	}
}

func TestSetManagedZoneKeysIsSortedAndStable(t *testing.T) {
	cm := testConfigMap(nil, "")
	if !SetManagedZoneKeys(cm, []string{"b.db", "a.db"}) {
		t.Error("setting the annotation should report a change")
	}
	if got := cm.Annotations[ManagedZoneKeysAnnotation]; got != "a.db,b.db" {
		t.Errorf("annotation = %q, want sorted %q", got, "a.db,b.db")
	}
	// Order must not matter, or the annotation would churn between reconciles.
	if SetManagedZoneKeys(cm, []string{"a.db", "b.db"}) {
		t.Error("the same key set in a different order should not report a change")
	}
}

func TestManagedZoneKeys(t *testing.T) {
	tests := []struct {
		name string
		cm   *corev1.ConfigMap
		want int
	}{
		{name: "nil configmap", cm: nil, want: 0},
		{name: "no annotations", cm: &corev1.ConfigMap{}, want: 0},
		{
			name: "empty annotation",
			cm:   &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{ManagedZoneKeysAnnotation: ""}}},
			want: 0,
		},
		{
			name: "two keys with whitespace",
			cm:   &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{ManagedZoneKeysAnnotation: "a.db, b.db"}}},
			want: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := len(ManagedZoneKeys(test.cm)); got != test.want {
				t.Errorf("ManagedZoneKeys() returned %d keys, want %d", got, test.want)
			}
		})
	}
}
