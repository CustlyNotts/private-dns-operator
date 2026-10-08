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

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ZoneReference points at the cluster-scoped PrivateDNSZone that owns a record.
type ZoneReference struct {
	// Name is the name of the PrivateDNSZone object.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// PrivateDNSRecordSpec defines a single delegated record in a private zone.
type PrivateDNSRecordSpec struct {
	// ZoneRef selects the PrivateDNSZone this record belongs to. The zone's
	// allowedNamespaces must permit this record's namespace.
	ZoneRef ZoneReference `json:"zoneRef"`

	// Name is the record name relative to the zone. Use "@" or an empty string
	// for the zone apex, or a fully qualified name ending in a dot that falls
	// inside the zone.
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Type is the DNS record type.
	Type DNSRecordType `json:"type"`

	// TTL overrides the zone TTL for this record, in seconds.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +optional
	TTL *int32 `json:"ttl,omitempty"`

	// Values holds the record data. The accepted format depends on Type:
	// an IPv4 address for A, an IPv6 address for AAAA, a hostname for CNAME,
	// free text for TXT, "<priority> <host>" for MX, and
	// "<priority> <weight> <port> <target>" for SRV.
	// +kubebuilder:validation:MinItems=1
	Values []string `json:"values"`
}

// PrivateDNSRecordStatus reports whether a record was accepted into its zone.
type PrivateDNSRecordStatus struct {
	// ObservedGeneration is the record generation these conditions describe.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions report whether the record was accepted and published.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// FQDN is the fully qualified name this record resolves, as rendered into
	// CoreDNS.
	// +optional
	FQDN string `json:"fqdn,omitempty"`
}

// PrivateDNSRecord is a namespaced record published into a PrivateDNSZone.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=pdr
// +kubebuilder:printcolumn:name="Zone",type=string,JSONPath=`.spec.zoneRef.name`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="FQDN",type=string,JSONPath=`.status.fqdn`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type PrivateDNSRecord struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   PrivateDNSRecordSpec   `json:"spec"`
	Status PrivateDNSRecordStatus `json:"status,omitempty"`
}

// PrivateDNSRecordList contains a list of PrivateDNSRecord.
// +kubebuilder:object:root=true
type PrivateDNSRecordList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrivateDNSRecord `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PrivateDNSRecord{}, &PrivateDNSRecordList{})
}
