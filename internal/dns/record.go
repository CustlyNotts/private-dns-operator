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

package dns

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

const DefaultTTL int32 = 300

type Source struct {
	Kind      string
	Namespace string
	Name      string
}

type Record struct {
	Zone   string
	Name   string
	FQDN   string
	Type   dnsv1alpha1.DNSRecordType
	TTL    int32
	Values []string
	Source Source
}

type ValidationError struct {
	Source  Source
	FQDN    string
	Message string
}

func (e ValidationError) Error() string {
	if e.Source.Namespace != "" {
		return fmt.Sprintf("%s/%s %s: %s", e.Source.Namespace, e.Source.Name, e.FQDN, e.Message)
	}
	return fmt.Sprintf("%s %s: %s", e.Source.Name, e.FQDN, e.Message)
}

func NormalizeZone(zone string) (string, error) {
	normalized := normalizeDomain(zone)
	if normalized == "." || normalized == "" {
		return "", fmt.Errorf("zone must not be empty")
	}
	if err := validateDomain(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func FQDN(name string, zone string) (string, error) {
	zone, err := NormalizeZone(zone)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "@" {
		return zone, nil
	}
	if strings.HasSuffix(name, ".") {
		fqdn := normalizeDomain(name)
		if !strings.HasSuffix(fqdn, "."+zone) && fqdn != zone {
			return "", fmt.Errorf("fqdn %q is outside zone %q", fqdn, zone)
		}
		if err := validateDomain(fqdn); err != nil {
			return "", err
		}
		return fqdn, nil
	}
	fqdn := normalizeDomain(name + "." + zone)
	if err := validateDomain(fqdn); err != nil {
		return "", err
	}
	return fqdn, nil
}

// ValidateRecord checks a record on its own: its TTL, that it carries values,
// and that each value is well formed for its type.
//
// This is deliberately separable from ValidateRecordSet. A delegated record that
// fails here is one tenant's mistake and can simply be excluded from the zone,
// whereas a set-level conflict is a property of the zone as a whole.
func ValidateRecord(record Record) []error {
	var errs []error
	if record.TTL <= 0 {
		errs = append(errs, ValidationError{Source: record.Source, FQDN: record.FQDN, Message: "ttl must be greater than zero"})
	}
	if len(record.Values) == 0 {
		errs = append(errs, ValidationError{Source: record.Source, FQDN: record.FQDN, Message: "at least one value is required"})
	}
	for _, value := range record.Values {
		if err := validateValue(record.Type, value); err != nil {
			errs = append(errs, ValidationError{Source: record.Source, FQDN: record.FQDN, Message: err.Error()})
		}
	}
	return errs
}

// ValidateRecordSet checks the constraints that exist only between records: a
// CNAME cannot share a name with other data, and cannot have several targets.
func ValidateRecordSet(records []Record) []error {
	var errs []error
	byFQDN := map[string][]Record{}
	for _, record := range records {
		byFQDN[record.FQDN] = append(byFQDN[record.FQDN], record)
	}

	fqdns := make([]string, 0, len(byFQDN))
	for fqdn := range byFQDN {
		fqdns = append(fqdns, fqdn)
	}
	sort.Strings(fqdns)

	for _, fqdn := range fqdns {
		set := byFQDN[fqdn]

		// What matters is how many *distinct* targets exist and whether any
		// other type shares the name, not how many objects happen to declare
		// them. Several records naming the same target describe one RRset and
		// are a compatible duplicate, exactly as they are for an A record.
		targets := map[string]struct{}{}
		var cnameSource *Record
		otherTypes := 0
		for i := range set {
			record := &set[i]
			if record.Type != dnsv1alpha1.DNSRecordTypeCNAME {
				otherTypes++
				continue
			}
			if cnameSource == nil {
				cnameSource = record
			}
			for _, value := range record.Values {
				targets[normalizeDomain(value)] = struct{}{}
			}
		}
		if len(targets) == 0 {
			continue
		}

		// Attribute the failure to the CNAME, since that is the record whose
		// constraint is being broken.
		source := cnameSource.Source
		switch {
		case otherTypes > 0:
			errs = append(errs, ValidationError{Source: source, FQDN: fqdn,
				Message: "CNAME cannot share a name with any other record type"})
		case len(targets) > 1:
			errs = append(errs, ValidationError{Source: source, FQDN: fqdn,
				Message: fmt.Sprintf("CNAME must have exactly one target, found %d", len(targets))})
		}
	}
	return errs
}

// ValidateRecords runs the per-record and set-level checks together.
func ValidateRecords(records []Record) []error {
	var errs []error
	for _, record := range records {
		errs = append(errs, ValidateRecord(record)...)
	}
	return append(errs, ValidateRecordSet(records)...)
}

func DeduplicateAndSort(records []Record) []Record {
	collapsed := map[string]Record{}
	for _, record := range records {
		values := uniqueSorted(record.Values)
		key := strings.Join([]string{
			record.FQDN,
			string(record.Type),
			strconv.Itoa(int(record.TTL)),
			record.Source.Kind,
			record.Source.Namespace,
			record.Source.Name,
		}, "|")
		record.Values = values
		collapsed[key] = record
	}

	out := make([]Record, 0, len(collapsed))
	for _, record := range collapsed {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FQDN != out[j].FQDN {
			return out[i].FQDN < out[j].FQDN
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Source.Name < out[j].Source.Name
	})
	return out
}

func normalizeDomain(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, ".")
	if value == "" {
		return "."
	}
	return value + "."
}

func validateDomain(value string) error {
	value = strings.TrimSuffix(value, ".")
	if len(value) > 253 {
		return fmt.Errorf("domain %q exceeds 253 characters", value)
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" {
			return fmt.Errorf("domain %q contains an empty label", value)
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q exceeds 63 characters", label)
		}
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Errorf("label %q must not start or end with '-'", label)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Errorf("label %q contains invalid character %q", label, r)
			}
		}
	}
	return nil
}

func validateValue(recordType dnsv1alpha1.DNSRecordType, value string) error {
	value = strings.TrimSpace(value)
	switch recordType {
	case dnsv1alpha1.DNSRecordTypeA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("value %q for an A record must be an IPv4 address", value)
		}
	case dnsv1alpha1.DNSRecordTypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("value %q for an AAAA record must be an IPv6 address", value)
		}
	case dnsv1alpha1.DNSRecordTypeCNAME:
		return validateDomain(normalizeDomain(value))
	case dnsv1alpha1.DNSRecordTypeTXT:
		if value == "" {
			return fmt.Errorf("value for a TXT record must not be empty")
		}
	case dnsv1alpha1.DNSRecordTypeMX:
		parts := strings.Fields(value)
		if len(parts) != 2 {
			return fmt.Errorf("value %q for an MX record must be '<priority> <host>'", value)
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			return fmt.Errorf("priority %q in an MX record must be an integer", parts[0])
		}
		return validateDomain(normalizeDomain(parts[1]))
	case dnsv1alpha1.DNSRecordTypeSRV:
		parts := strings.Fields(value)
		if len(parts) != 4 {
			return fmt.Errorf("value %q for an SRV record must be '<priority> <weight> <port> <target>'", value)
		}
		for _, part := range parts[:3] {
			if _, err := strconv.Atoi(part); err != nil {
				return fmt.Errorf("numeric field %q in an SRV record must be an integer", part)
			}
		}
		return validateDomain(normalizeDomain(parts[3]))
	default:
		return fmt.Errorf("unsupported record type %q", recordType)
	}
	return nil
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
