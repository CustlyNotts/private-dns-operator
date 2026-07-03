package coredns

import (
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func EnsureZoneVolumeItems(deployment *appsv1.Deployment, configMapName string, zoneKeys []string) (bool, error) {
	sort.Strings(zoneKeys)
	changed := false
	for vi := range deployment.Spec.Template.Spec.Volumes {
		volume := &deployment.Spec.Template.Spec.Volumes[vi]
		if volume.ConfigMap == nil || volume.ConfigMap.Name != configMapName {
			continue
		}
		if len(volume.ConfigMap.Items) == 0 {
			return false, nil
		}
		existing := map[string]corev1.KeyToPath{}
		for _, item := range volume.ConfigMap.Items {
			existing[item.Key] = item
		}
		for _, key := range zoneKeys {
			if _, ok := existing[key]; !ok {
				volume.ConfigMap.Items = append(volume.ConfigMap.Items, corev1.KeyToPath{Key: key, Path: key})
				changed = true
			}
		}
		filtered := volume.ConfigMap.Items[:0]
		for _, item := range volume.ConfigMap.Items {
			if IsManagedZoneKey(item.Key) && !contains(zoneKeys, item.Key) {
				changed = true
				continue
			}
			filtered = append(filtered, item)
		}
		volume.ConfigMap.Items = filtered
		sort.Slice(volume.ConfigMap.Items, func(i, j int) bool {
			return volume.ConfigMap.Items[i].Key < volume.ConfigMap.Items[j].Key
		})
		return changed, nil
	}
	return false, fmt.Errorf("deployment does not mount ConfigMap %q", configMapName)
}

func MarkRolloutRestart(deployment *appsv1.Deployment, reason string, value string) {
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["dns.custlynotts.io/restarted-for"] = reason
	deployment.Spec.Template.Annotations["dns.custlynotts.io/restart-hash"] = value
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
