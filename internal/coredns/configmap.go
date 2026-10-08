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
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// ManagedZoneKeysAnnotation records which ConfigMap keys the operator owns.
// It is the only authority on that question: the operator never infers
// ownership from a key's name, so a hand-maintained zone file living in the
// same ConfigMap is left alone.
//
// The annotation is always a superset of the keys currently rendered. It is
// widened before a key is created and narrowed only after a key is deleted, so
// an operator that dies mid-sequence can still find everything it owns.
const ManagedZoneKeysAnnotation = "dns.custlynotts.io/managed-zone-keys"

// corefileKey is the ConfigMap key holding the CoreDNS configuration.
const corefileKey = "Corefile"

// The functions below are deliberately single-purpose rather than one
// apply-everything call. CoreDNS mounts this ConfigMap with explicit volume
// items, which imposes two invariants the operator has to respect in opposite
// orders:
//
//   - a key named in the Deployment's volume items must exist in the ConfigMap,
//     or the volume fails to populate and new CoreDNS pods never start
//   - a file named by the Corefile's file plugin must be mounted in the
//     container, or CoreDNS exits during startup
//
// Creating a zone therefore needs the ConfigMap written first, and removing one
// needs the Deployment written first. Keeping the steps separate lets the
// reconciler sequence them so both invariants hold at every point in between.

// SetZoneFiles adds and updates rendered zone files. It never removes a key, so
// it is safe to call before the Deployment's volume items have caught up.
func SetZoneFiles(cm *corev1.ConfigMap, zoneFiles map[string]string) bool {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	changed := false
	for key, content := range zoneFiles {
		if existing, ok := cm.Data[key]; !ok || existing != content {
			cm.Data[key] = content
			changed = true
		}
	}
	return changed
}

// SetCorefile replaces the CoreDNS configuration. Call it only once every file
// the new Corefile references is mounted in the CoreDNS container.
func SetCorefile(cm *corev1.ConfigMap, corefile string) bool {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Data[corefileKey] == corefile {
		return false
	}
	cm.Data[corefileKey] = corefile
	return true
}

// RemoveZoneFiles deletes the named keys. Call it only once nothing references
// them: not the Corefile, and not the Deployment's volume items.
func RemoveZoneFiles(cm *corev1.ConfigMap, keys []string) bool {
	if cm.Data == nil {
		return false
	}
	changed := false
	for _, key := range keys {
		if _, ok := cm.Data[key]; ok {
			delete(cm.Data, key)
			changed = true
		}
	}
	return changed
}

// SetManagedZoneKeys records the keys the operator owns. Widen this before
// creating a key and narrow it only after deleting one, so a reconcile that
// fails partway through never loses track of a key it has to clean up later.
func SetManagedZoneKeys(cm *corev1.ConfigMap, keys []string) bool {
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	value := strings.Join(sorted, ",")
	if cm.Annotations[ManagedZoneKeysAnnotation] == value {
		return false
	}
	cm.Annotations[ManagedZoneKeysAnnotation] = value
	return true
}

// ManagedZoneKeys returns the ConfigMap keys the operator owns, as recorded by
// the last reconcile that touched the annotation.
func ManagedZoneKeys(cm *corev1.ConfigMap) []string {
	if cm == nil || cm.Annotations == nil {
		return nil
	}
	raw := cm.Annotations[ManagedZoneKeysAnnotation]
	if raw == "" {
		return nil
	}
	var keys []string
	for _, key := range strings.Split(raw, ",") {
		key = strings.TrimSpace(key)
		if key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}
