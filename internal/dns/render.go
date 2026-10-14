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
	"crypto/sha1"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

type ZoneFile struct {
	Key     string
	Content string
	Serial  string

	// Hash digests the zone body with the serial held out, so it changes only
	// when the records do. That is what lets an unchanged zone keep its serial.
	Hash string
}

// PreviousRender is what the last successful render of a zone produced. The
// serial has to be carried forward because CoreDNS's file plugin reloads a zone
// only when the SOA serial increases, so a new serial must be greater than the
// one CoreDNS is already serving.
type PreviousRender struct {
	Serial string
	Hash   string
}

// serialPlaceholder stands in for the serial while the body is hashed. The
// serial cannot be part of its own input, and hashing the body without it is
// what keeps an unchanged zone byte-identical across reconciles: a serial that
// moved every pass would change the content, which would write status, which
// would retrigger the watch that scheduled the reconcile.
const serialPlaceholder = "@@SERIAL@@"

// nextSerial returns a serial strictly greater than previous, preferring the
// conventional YYYYMMDDnn form.
//
// Monotonicity is the hard requirement, readability only a preference: CoreDNS
// ignores a zone whose serial did not advance, so going backwards would strand
// it on stale records until the next restart.
func nextSerial(previous string, now time.Time) string {
	candidate := now.UTC().Format("20060102") + "01"

	prev, err := strconv.ParseUint(previous, 10, 32)
	if err != nil {
		return candidate
	}
	if value, err := strconv.ParseUint(candidate, 10, 32); err == nil && value > prev {
		return candidate
	}

	// Same day as the last render, or the clock went backwards. Increment rather
	// than reuse, so the change is still visible to CoreDNS.
	if prev >= math.MaxUint32 {
		return strconv.FormatUint(math.MaxUint32, 10)
	}
	return strconv.FormatUint(prev+1, 10)
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

func RenderZone(zone dnsv1alpha1.PrivateDNSZone, records []Record, now time.Time, previous PreviousRender) (ZoneFile, error) {
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

	var b strings.Builder
	fmt.Fprintf(&b, "$ORIGIN %s\n", normalizedZone)
	fmt.Fprintf(&b, "$TTL %d\n", ttl)
	fmt.Fprintf(&b, "@ IN SOA ns.dns.%s hostmaster.%s (\n", normalizedZone, normalizedZone)
	fmt.Fprintf(&b, "  %s ; serial\n", serialPlaceholder)
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

	body := b.String()
	sum := sha1.Sum([]byte(body))
	hash := fmt.Sprintf("%x", sum[:])

	// Reuse the serial when the records are unchanged, so a steady-state
	// reconcile renders byte-identical output and writes nothing.
	serial := previous.Serial
	if serial == "" || hash != previous.Hash {
		serial = nextSerial(previous.Serial, now)
	}

	return ZoneFile{
		Key:     ZoneFileKey(normalizedZone),
		Content: strings.Replace(body, serialPlaceholder, serial, 1),
		Serial:  serial,
		Hash:    hash,
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
