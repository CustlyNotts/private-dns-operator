package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func condition(conditionType string, status metav1.ConditionStatus, reason string, message string, generation int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: generation,
		LastTransitionTime: metav1.Now(),
	}
}
