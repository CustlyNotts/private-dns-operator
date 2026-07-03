package dns

import (
	"crypto/sha1"
	"fmt"
	"sort"
	"strings"
	"time"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

type ZoneFile struct {
	Key     string
	Content string
	Serial  string
	Hash    string
}

func ZoneFileKey(zone string) string {
	normalized, err := NormalizeZone(zone)
	if err != nil {
		normalized = strings.ToLower(strings.Trim(zone, "."))
	}
	base := strings.TrimSuffix(normalized, ".")
	base = strings.NewReplacer(".", "-", "_", "-").Replace(base)
	sum := sha1.Sum([]byte(normalized))
	return fmt.Sprintf("%s-%x.db", base, sum[:3])
}

func RenderZone(zone dnsv1alpha1.PrivateDNSZone, records []Record, now time.Time) (ZoneFile, error) {
	normalizedZone, err := NormalizeZone(zone.Spec.Zone)
	if err != nil {
		return ZoneFile{}, err
	}
	ttl := DefaultTTL
	if zone.Spec.TTL != nil {
		ttl = *zone.Spec.TTL
	}
	if ttl <= 0 {
		return ZoneFile{}, fmt.Errorf("zone ttl must be greater than zero")
	}

	records = DeduplicateAndSort(records)
	if errs := ValidateRecords(records); len(errs) > 0 {
		return ZoneFile{}, errs[0]
	}

	serial := now.UTC().Format("2006010201")
	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN %s\n", normalizedZone)
	fmt.Fprintf(&b, "$TTL %d\n", ttl)
	fmt.Fprintf(&b, "@ IN SOA ns.dns.%s hostmaster.%s (\n", normalizedZone, normalizedZone)
	fmt.Fprintf(&b, "  %s ; serial\n", serial)
	b.WriteString("  7200       ; refresh\n")
	b.WriteString("  3600       ; retry\n")
	b.WriteString("  1209600    ; expire\n")
	b.WriteString("  3600       ; minimum\n")
	b.WriteString(")\n")
	fmt.Fprintf(&b, "@ IN NS ns.dns.%s\n", normalizedZone)

	for _, record := range records {
		relativeName := relativeName(record.FQDN, normalizedZone)
		values := append([]string(nil), record.Values...)
		sort.Strings(values)
		for _, value := range values {
			fmt.Fprintf(&b, "%s %d IN %s %s\n", relativeName, record.TTL, record.Type, renderValue(record.Type, value))
		}
	}

	content := b.String()
	sum := sha1.Sum([]byte(content))
	return ZoneFile{
		Key:     ZoneFileKey(normalizedZone),
		Content: content,
		Serial:  serial,
		Hash:    fmt.Sprintf("%x", sum[:]),
	}, nil
}

func relativeName(fqdn string, zone string) string {
	if fqdn == zone {
		return "@"
	}
	return strings.TrimSuffix(strings.TrimSuffix(fqdn, zone), ".")
}

func renderValue(recordType dnsv1alpha1.DNSRecordType, value string) string {
	value = strings.TrimSpace(value)
	switch recordType {
	case dnsv1alpha1.DNSRecordTypeCNAME:
		return ensureTrailingDot(value)
	case dnsv1alpha1.DNSRecordTypeTXT:
		return fmt.Sprintf("%q", value)
	case dnsv1alpha1.DNSRecordTypeMX:
		parts := strings.Fields(value)
		if len(parts) == 2 {
			return parts[0] + " " + ensureTrailingDot(parts[1])
		}
	case dnsv1alpha1.DNSRecordTypeSRV:
		parts := strings.Fields(value)
		if len(parts) == 4 {
			return strings.Join(parts[:3], " ") + " " + ensureTrailingDot(parts[3])
		}
	}
	return value
}

func ensureTrailingDot(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}
