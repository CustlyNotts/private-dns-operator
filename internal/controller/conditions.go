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

package controller

import (
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types reported on PrivateDNSZone.
const (
	ConditionTemplateRendered     = "TemplateRendered"
	ConditionZoneFileRendered     = "ZoneFileRendered"
	ConditionCorefilePatched      = "CorefilePatched"
	ConditionVolumeMounted        = "VolumeMounted"
	ConditionReloadTriggered      = "ReloadTriggered"
	ConditionLastKnownGoodApplied = "LastKnownGoodApplied"
	ConditionReady                = "Ready"
)

// ConditionAccepted is reported on PrivateDNSRecord.
const ConditionAccepted = "Accepted"

// zoneConditionTypes is every condition type the operator owns on a zone. Any
// of these missing from a reconcile's desired set is pruned, so a condition
// such as LastKnownGoodApplied disappears once the zone recovers.
var zoneConditionTypes = []string{
	ConditionTemplateRendered,
	ConditionZoneFileRendered,
	ConditionCorefilePatched,
	ConditionVolumeMounted,
	ConditionReloadTriggered,
	ConditionLastKnownGoodApplied,
	ConditionReady,
}

func condition(conditionType string, status metav1.ConditionStatus, reason string, message string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
	}
}

// applyConditions merges desired into existing, then prunes any owned type that
// is no longer desired.
//
// It goes through apimeta.SetStatusCondition deliberately: that helper keeps the
// stored LastTransitionTime when a condition's status has not changed. Stamping
// a fresh timestamp on every pass would make each status write a real object
// change, which would re-trigger the watch and spin the reconciler forever.
func applyConditions(existing []metav1.Condition, desired []metav1.Condition, owned []string) []metav1.Condition {
	conditions := append([]metav1.Condition(nil), existing...)
	wanted := map[string]struct{}{}
	for _, cond := range desired {
		wanted[cond.Type] = struct{}{}
		apimeta.SetStatusCondition(&conditions, cond)
	}
	for _, conditionType := range owned {
		if _, keep := wanted[conditionType]; !keep {
			apimeta.RemoveStatusCondition(&conditions, conditionType)
		}
	}
	return conditions
}
