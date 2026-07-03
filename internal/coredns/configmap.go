package coredns

import (
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const ManagedZoneKeysAnnotation = "dns.custlynotts.io/managed-zone-keys"

func ApplyConfigMap(cm *corev1.ConfigMap, corefile string, zoneFiles map[string]string) {
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	cm.Data["Corefile"] = corefile

	for _, key := range managedZoneKeys(cm) {
		if _, ok := zoneFiles[key]; !ok {
			delete(cm.Data, key)
		}
	}
	for key, content := range zoneFiles {
		cm.Data[key] = content
	}
	cm.Annotations[ManagedZoneKeysAnnotation] = strings.Join(sortedKeys(zoneFiles), ",")
}

func IsManagedZoneKey(key string) bool {
	return strings.HasSuffix(key, ".db")
}

func managedZoneKeys(cm *corev1.ConfigMap) []string {
	if cm.Annotations == nil {
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

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
