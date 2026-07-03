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

func ValidateRecords(records []Record) []error {
	var errs []error
	byFQDN := map[string][]Record{}
	for _, record := range records {
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
		byFQDN[record.FQDN] = append(byFQDN[record.FQDN], record)
	}

	for fqdn, set := range byFQDN {
		cnameCount := 0
		for _, record := range set {
			if record.Type == dnsv1alpha1.DNSRecordTypeCNAME {
				cnameCount += len(record.Values)
			}
		}
		if cnameCount > 0 && (len(set) > 1 || cnameCount > 1 || len(set[0].Values) > 1) {
			errs = append(errs, ValidationError{Source: set[0].Source, FQDN: fqdn, Message: "CNAME cannot coexist with other records or multiple CNAME values"})
		}
	}
	return errs
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
			return fmt.Errorf("A record value %q must be an IPv4 address", value)
		}
	case dnsv1alpha1.DNSRecordTypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("AAAA record value %q must be an IPv6 address", value)
		}
	case dnsv1alpha1.DNSRecordTypeCNAME:
		return validateDomain(normalizeDomain(value))
	case dnsv1alpha1.DNSRecordTypeTXT:
		if value == "" {
			return fmt.Errorf("TXT record value must not be empty")
		}
	case dnsv1alpha1.DNSRecordTypeMX:
		parts := strings.Fields(value)
		if len(parts) != 2 {
			return fmt.Errorf("MX record value %q must be '<priority> <host>'", value)
		}
		if _, err := strconv.Atoi(parts[0]); err != nil {
			return fmt.Errorf("MX priority %q must be an integer", parts[0])
		}
		return validateDomain(normalizeDomain(parts[1]))
	case dnsv1alpha1.DNSRecordTypeSRV:
		parts := strings.Fields(value)
		if len(parts) != 4 {
			return fmt.Errorf("SRV record value %q must be '<priority> <weight> <port> <target>'", value)
		}
		for _, part := range parts[:3] {
			if _, err := strconv.Atoi(part); err != nil {
				return fmt.Errorf("SRV numeric field %q must be an integer", part)
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
