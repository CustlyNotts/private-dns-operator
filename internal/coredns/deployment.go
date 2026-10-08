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

package coredns

import (
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	restartReasonAnnotation = "dns.custlynotts.io/restarted-for"
	restartHashAnnotation   = "dns.custlynotts.io/restart-hash"
)

// EnsureZoneVolumeItems reconciles the explicit ConfigMap volume items CoreDNS
// mounts so every rendered zone file is visible in the container.
//
// The reconciler calls this twice per commit: once with the union of the old and
// new key sets, to mount everything either Corefile could name, and again with
// just the new set once the Corefile no longer names the retired keys.
//
// desiredKeys are the zone file keys that should be mounted now.
// previouslyManaged are the keys the operator claimed on its last reconcile,
// read from the managed-zone-keys annotation. An item is removed only when it
// appears in previouslyManaged and no longer in desiredKeys, so a zone file
// someone else added to the same ConfigMap is never unmounted.
//
// A volume that mounts the ConfigMap without explicit items already exposes
// every key, so it is left untouched.
func EnsureZoneVolumeItems(deployment *appsv1.Deployment, configMapName string, desiredKeys []string, previouslyManaged []string) (bool, error) {
	desired := append([]string(nil), desiredKeys...)
	sort.Strings(desired)
	changed := false

	for vi := range deployment.Spec.Template.Spec.Volumes {
		volume := &deployment.Spec.Template.Spec.Volumes[vi]
		if volume.ConfigMap == nil || volume.ConfigMap.Name != configMapName {
			continue
		}
		if len(volume.ConfigMap.Items) == 0 {
			return false, nil
		}

		existing := map[string]struct{}{}
		for _, item := range volume.ConfigMap.Items {
			existing[item.Key] = struct{}{}
		}
		for _, key := range desired {
			if _, ok := existing[key]; !ok {
				volume.ConfigMap.Items = append(volume.ConfigMap.Items, corev1.KeyToPath{Key: key, Path: key})
				changed = true
			}
		}

		stale := map[string]struct{}{}
		for _, key := range previouslyManaged {
			if !contains(desired, key) {
				stale[key] = struct{}{}
			}
		}
		filtered := volume.ConfigMap.Items[:0]
		for _, item := range volume.ConfigMap.Items {
			if _, drop := stale[item.Key]; drop {
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

// MarkRolloutRestart stamps the pod template so CoreDNS restarts, used when the
// reload plugin is absent or the mounted zone files changed.
func MarkRolloutRestart(deployment *appsv1.Deployment, reason string, value string) {
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations[restartReasonAnnotation] = reason
	deployment.Spec.Template.Annotations[restartHashAnnotation] = value
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
