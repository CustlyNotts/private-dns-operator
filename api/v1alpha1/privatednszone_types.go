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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UnresolvedRecordPolicy controls how CoreDNS answers names inside a private
// zone that the operator has not rendered a record for.
// +kubebuilder:validation:Enum=Forward;NXDOMAIN
type UnresolvedRecordPolicy string

const (
	// UnresolvedRecordPolicyNXDOMAIN renders the zone with the CoreDNS file
	// plugin, making it a strict authoritative private zone.
	UnresolvedRecordPolicyNXDOMAIN UnresolvedRecordPolicy = "NXDOMAIN"
	// UnresolvedRecordPolicyForward renders declared records as CoreDNS
	// template stanzas with fallthrough, then forwards everything else
	// upstream.
	UnresolvedRecordPolicyForward UnresolvedRecordPolicy = "Forward"
)

// DNSRecordType is a supported DNS resource record type.
// +kubebuilder:validation:Enum=A;AAAA;CNAME;TXT;MX;SRV
type DNSRecordType string

const (
	DNSRecordTypeA     DNSRecordType = "A"
	DNSRecordTypeAAAA  DNSRecordType = "AAAA"
	DNSRecordTypeCNAME DNSRecordType = "CNAME"
	DNSRecordTypeTXT   DNSRecordType = "TXT"
	DNSRecordTypeMX    DNSRecordType = "MX"
	DNSRecordTypeSRV   DNSRecordType = "SRV"
)

// NamespaceSelector restricts which namespaces may contribute records to a zone.
type NamespaceSelector struct {
	// MatchNames lists namespaces allowed to contribute PrivateDNSRecord objects.
	// An empty list allows every namespace, so set this explicitly on any zone
	// exposed to tenants.
	// +optional
	MatchNames []string `json:"matchNames,omitempty"`
}

// PrivateDNSRecordSpecInline is a record declared directly on the zone, for
// records the zone owner does not want to delegate.
type PrivateDNSRecordSpecInline struct {
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

// PrivateDNSZoneSpec defines the desired state of a private DNS zone.
type PrivateDNSZoneSpec struct {
	// Zone is the DNS suffix this resource owns, such as "platform.internal".
	// A trailing dot is optional.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Zone string `json:"zone"`

	// TTL is the default record TTL for the zone, in seconds. Defaults to 300
	// when unset.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +optional
	TTL *int32 `json:"ttl,omitempty"`

	// UnresolvedRecordPolicy selects how CoreDNS answers undeclared names in
	// the zone. Defaults to NXDOMAIN when unset.
	// +optional
	UnresolvedRecordPolicy UnresolvedRecordPolicy `json:"unresolvedRecordPolicy,omitempty"`

	// AllowedNamespaces restricts which namespaces may publish
	// PrivateDNSRecord objects into this zone.
	// +optional
	AllowedNamespaces NamespaceSelector `json:"allowedNamespaces,omitempty"`

	// Records declares records inline on the zone, in addition to any
	// PrivateDNSRecord objects that reference it.
	// +optional
	Records []PrivateDNSRecordSpecInline `json:"records,omitempty"`
}

// PrivateDNSZoneStatus reports the observed state of a private DNS zone.
type PrivateDNSZoneStatus struct {
	// ObservedGeneration is the zone generation these conditions describe.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions report progress through render, patch, mount, and reload.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Records is the number of rendered record values in the zone.
	// +optional
	Records int `json:"records,omitempty"`

	// Serial is the SOA serial of the most recently rendered zone file.
	// +optional
	Serial string `json:"serial,omitempty"`

	// ZoneFileKey is the CoreDNS ConfigMap key holding the rendered zone file.
	// Only set for the NXDOMAIN policy.
	// +optional
	ZoneFileKey string `json:"zoneFileKey,omitempty"`

	// LastAppliedHash is a digest of the rendered zone output, used to decide
	// whether a CoreDNS restart is required.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`
}

// PrivateDNSZone is a private DNS zone rendered into CoreDNS.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=pdz
// +kubebuilder:printcolumn:name="Zone",type=string,JSONPath=`.spec.zone`
// +kubebuilder:printcolumn:name="Policy",type=string,JSONPath=`.spec.unresolvedRecordPolicy`
// +kubebuilder:printcolumn:name="Records",type=integer,JSONPath=`.status.records`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type PrivateDNSZone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   PrivateDNSZoneSpec   `json:"spec"`
	Status PrivateDNSZoneStatus `json:"status,omitempty"`
}

// PrivateDNSZoneList contains a list of PrivateDNSZone.
// +kubebuilder:object:root=true
type PrivateDNSZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PrivateDNSZone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PrivateDNSZone{}, &PrivateDNSZoneList{})
}
