package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type ZoneReference struct {
	Name string `json:"name"`
}

type PrivateDNSRecordSpec struct {
	ZoneRef ZoneReference `json:"zoneRef"`
	Name    string        `json:"name"`
	Type    DNSRecordType `json:"type"`
	TTL     *int32        `json:"ttl,omitempty"`
	Values  []string      `json:"values"`
}

type PrivateDNSRecordStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	FQDN               string             `json:"fqdn,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:scope=Namespaced
type PrivateDNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PrivateDNSRecordSpec   `json:"spec,omitempty"`
	Status PrivateDNSRecordStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type PrivateDNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrivateDNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PrivateDNSRecord{}, &PrivateDNSRecordList{})
}
