package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UnresolvedRecordPolicy string

const (
	UnresolvedRecordPolicyNXDOMAIN UnresolvedRecordPolicy = "NXDOMAIN"
	UnresolvedRecordPolicyForward  UnresolvedRecordPolicy = "Forward"
)

type DNSRecordType string

const (
	DNSRecordTypeA     DNSRecordType = "A"
	DNSRecordTypeAAAA  DNSRecordType = "AAAA"
	DNSRecordTypeCNAME DNSRecordType = "CNAME"
	DNSRecordTypeTXT   DNSRecordType = "TXT"
	DNSRecordTypeMX    DNSRecordType = "MX"
	DNSRecordTypeSRV   DNSRecordType = "SRV"
)

type NamespaceSelector struct {
	// MatchNames lists namespaces allowed to contribute PrivateDNSRecord objects.
	// Empty means all namespaces are allowed.
	MatchNames []string `json:"matchNames,omitempty"`
}

type PrivateDNSRecordSpecInline struct {
	Name   string        `json:"name"`
	Type   DNSRecordType `json:"type"`
	TTL    *int32        `json:"ttl,omitempty"`
	Values []string      `json:"values"`
}

type PrivateDNSZoneSpec struct {
	Zone                   string                       `json:"zone"`
	TTL                    *int32                       `json:"ttl,omitempty"`
	UnresolvedRecordPolicy UnresolvedRecordPolicy       `json:"unresolvedRecordPolicy,omitempty"`
	AllowedNamespaces      NamespaceSelector            `json:"allowedNamespaces,omitempty"`
	Records                []PrivateDNSRecordSpecInline `json:"records,omitempty"`
}

type PrivateDNSZoneStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Records            int                `json:"records,omitempty"`
	Serial             string             `json:"serial,omitempty"`
	ZoneFileKey        string             `json:"zoneFileKey,omitempty"`
	LastAppliedHash    string             `json:"lastAppliedHash,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:scope=Cluster
type PrivateDNSZone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PrivateDNSZoneSpec   `json:"spec,omitempty"`
	Status PrivateDNSZoneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type PrivateDNSZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrivateDNSZone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PrivateDNSZone{}, &PrivateDNSZoneList{})
}
