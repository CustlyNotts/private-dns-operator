package coredns

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func corednsDeployment(items ...corev1.KeyToPath) *appsv1.Deployment {
	return &appsv1.Deployment{
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{{
						Name: "config-volume",
						VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "coredns"},
								Items:                items,
							},
						},
					}},
				},
			},
		},
	}
}

func itemKeys(deployment *appsv1.Deployment) []string {
	var keys []string
	for _, item := range deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Items {
		keys = append(keys, item.Key)
	}
	return keys
}

func TestEnsureZoneVolumeItemsAddsMissingKeys(t *testing.T) {
	deployment := corednsDeployment(corev1.KeyToPath{Key: "Corefile", Path: "Corefile"})

	changed, err := EnsureZoneVolumeItems(deployment, "coredns", []string{"zone-a1b2c3.db"}, nil)
	if err != nil {
		t.Fatalf("EnsureZoneVolumeItems: %v", err)
	}
	if !changed {
		t.Error("adding a new zone file should report a change")
	}
	if got := itemKeys(deployment); len(got) != 2 || got[0] != "Corefile" || got[1] != "zone-a1b2c3.db" {
		t.Errorf("items = %v, want sorted [Corefile zone-a1b2c3.db]", got)
	}
}

func TestEnsureZoneVolumeItemsLeavesUnmanagedKeysAlone(t *testing.T) {
	// "handwritten.db" ends in .db but was never claimed by the operator, so it
	// must stay mounted even though it is not in the desired set.
	deployment := corednsDeployment(
		corev1.KeyToPath{Key: "Corefile", Path: "Corefile"},
		corev1.KeyToPath{Key: "handwritten.db", Path: "handwritten.db"},
		corev1.KeyToPath{Key: "retired-a1b2c3.db", Path: "retired-a1b2c3.db"},
	)

	changed, err := EnsureZoneVolumeItems(deployment, "coredns", nil, []string{"retired-a1b2c3.db"})
	if err != nil {
		t.Fatalf("EnsureZoneVolumeItems: %v", err)
	}
	if !changed {
		t.Error("removing a previously managed key should report a change")
	}
	got := itemKeys(deployment)
	if len(got) != 2 || got[0] != "Corefile" || got[1] != "handwritten.db" {
		t.Errorf("items = %v, want [Corefile handwritten.db]", got)
	}
}

func TestEnsureZoneVolumeItemsSkipsVolumesWithoutExplicitItems(t *testing.T) {
	// A volume with no items already exposes every ConfigMap key, so there is
	// nothing to patch and nothing to break.
	deployment := corednsDeployment()

	changed, err := EnsureZoneVolumeItems(deployment, "coredns", []string{"zone-a1b2c3.db"}, nil)
	if err != nil {
		t.Fatalf("EnsureZoneVolumeItems: %v", err)
	}
	if changed {
		t.Error("a volume without explicit items should not be modified")
	}
	if len(deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Items) != 0 {
		t.Error("items should remain empty")
	}
}

func TestEnsureZoneVolumeItemsErrorsWhenConfigMapIsNotMounted(t *testing.T) {
	deployment := corednsDeployment(corev1.KeyToPath{Key: "Corefile", Path: "Corefile"})
	deployment.Spec.Template.Spec.Volumes[0].ConfigMap.Name = "something-else"

	if _, err := EnsureZoneVolumeItems(deployment, "coredns", nil, nil); err == nil {
		t.Fatal("expected an error when the target ConfigMap is not mounted")
	}
}

func TestMarkRolloutRestart(t *testing.T) {
	deployment := corednsDeployment()
	MarkRolloutRestart(deployment, "private-dns-zone-change", "abc123")

	annotations := deployment.Spec.Template.Annotations
	if annotations[restartReasonAnnotation] != "private-dns-zone-change" {
		t.Errorf("restart reason = %q", annotations[restartReasonAnnotation])
	}
	if annotations[restartHashAnnotation] != "abc123" {
		t.Errorf("restart hash = %q", annotations[restartHashAnnotation])
	}
}
