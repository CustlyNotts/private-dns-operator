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

package coredns

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	dnsrender "github.com/custlynotts/private-dns-operator/internal/dns"
)

const (
	// ManagedBlockStart and ManagedBlockEnd delimit the only region of the
	// Corefile the operator ever writes.
	ManagedBlockStart = "# BEGIN private-dns-zone-operator"
	ManagedBlockEnd   = "# END private-dns-zone-operator"
)

// zoneStanzaHeader matches the opening line of a rendered server block, for
// example "platform.internal:53 {".
var zoneStanzaHeader = regexp.MustCompile(`^(\S+):53 \{$`)

// ZoneDirective is one CoreDNS server block to render inside the managed block.
type ZoneDirective struct {
	Zone    string
	DBFile  string
	Policy  dnsv1alpha1.UnresolvedRecordPolicy
	Records []dnsrender.Record

	// Raw carries a previously rendered stanza forward verbatim. It is set
	// when a zone's desired state is invalid and the operator is holding the
	// last known good output rather than dropping the zone from DNS. When Raw
	// is non-empty the other rendering fields are ignored.
	Raw string
}

// PatchCorefile replaces the managed block in corefile with one rendered from
// directives, leaving everything outside the block markers untouched.
func PatchCorefile(corefile string, directives []ZoneDirective) (string, error) {
	block := renderManagedBlock(directives)
	stripped, err := removeManagedBlock(corefile)
	if err != nil {
		return "", err
	}
	stripped = strings.TrimRight(stripped, "\n")
	return stripped + "\n\n" + block + "\n", nil
}

// ExtractZoneBlocks returns the stanza currently rendered for each zone inside
// the managed block, keyed by zone name without a trailing dot. The operator
// uses it to hold a zone's last known good output when its desired state stops
// being valid.
func ExtractZoneBlocks(corefile string) map[string]string {
	blocks := map[string]string{}
	start := strings.Index(corefile, ManagedBlockStart)
	end := strings.Index(corefile, ManagedBlockEnd)
	if start == -1 || end == -1 || end < start {
		return blocks
	}

	interior := corefile[start+len(ManagedBlockStart) : end]
	var zone string
	var current []string
	for _, line := range strings.Split(interior, "\n") {
		if zone == "" {
			if match := zoneStanzaHeader.FindStringSubmatch(line); match != nil {
				zone = match[1]
				current = []string{line}
			}
			continue
		}
		current = append(current, line)
		if line == "}" {
			blocks[zone] = strings.Join(current, "\n")
			zone = ""
			current = nil
		}
	}
	return blocks
}

// HasReloadPlugin reports whether the Corefile enables the reload plugin, which
// lets CoreDNS pick up changes without a pod restart.
func HasReloadPlugin(corefile string) bool {
	for _, line := range strings.Split(corefile, "\n") {
		line = strings.TrimSpace(line)
		if line == "reload" || strings.HasPrefix(line, "reload ") {
			return true
		}
	}
	return false
}

func renderManagedBlock(directives []ZoneDirective) string {
	sorted := append([]ZoneDirective(nil), directives...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Zone < sorted[j].Zone
	})

	var b strings.Builder
	b.WriteString(ManagedBlockStart)
	for _, directive := range sorted {
		if directive.Raw != "" {
			b.WriteString("\n\n")
			b.WriteString(strings.TrimRight(directive.Raw, "\n"))
			continue
		}
		zone := strings.TrimSuffix(directive.Zone, ".")
		fmt.Fprintf(&b, "\n\n%s:53 {\n    errors\n", zone)
		if directive.Policy == dnsv1alpha1.UnresolvedRecordPolicyForward {
			b.WriteString(renderTemplateRecords(directive.Records))
			b.WriteString("    forward . /etc/resolv.conf\n")
		} else {
			fmt.Fprintf(&b, "    file /etc/coredns/%s %s\n", directive.DBFile, zone)
		}
		b.WriteString("    cache 30\n}")
	}
	b.WriteString("\n")
	b.WriteString(ManagedBlockEnd)
	return b.String()
}

// templateRRSet is every value published at one name for one record type.
type templateRRSet struct {
	FQDN   string
	Zone   string
	Type   dnsv1alpha1.DNSRecordType
	TTL    int32
	Values []string
}

// groupTemplateRecords collapses records that share a name and type into a
// single RRset.
//
// This grouping is required, not cosmetic. CoreDNS evaluates template stanzas in
// order and the first one whose match expression hits answers the query and
// returns; fallthrough only applies when no expression matched at all. Emitting
// one stanza per source object would therefore publish only the first of several
// records at the same name, silently dropping the rest.
func groupTemplateRecords(records []dnsrender.Record) []templateRRSet {
	sets := map[string]*templateRRSet{}
	var keys []string

	for _, record := range records {
		fqdn := ensureTrailingDot(strings.TrimSpace(record.FQDN))
		key := fqdn + "|" + string(record.Type)
		set, ok := sets[key]
		if !ok {
			set = &templateRRSet{FQDN: fqdn, Zone: record.Zone, Type: record.Type, TTL: record.TTL}
			sets[key] = set
			keys = append(keys, key)
		}
		// RFC 2181 section 5.2: the records in one RRset should share a TTL, and
		// the lowest declared value is the safe choice when they disagree.
		if record.TTL < set.TTL {
			set.TTL = record.TTL
		}
		set.Values = append(set.Values, record.Values...)
	}

	sort.Strings(keys)
	out := make([]templateRRSet, 0, len(keys))
	for _, key := range keys {
		set := sets[key]
		set.Values = uniqueSortedValues(set.Values)
		out = append(out, *set)
	}
	return out
}

func uniqueSortedValues(values []string) []string {
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

func renderTemplateRecords(records []dnsrender.Record) string {
	var b strings.Builder
	for _, set := range groupTemplateRecords(dnsrender.DeduplicateAndSort(records)) {
		zone := strings.TrimSuffix(set.Zone, ".")
		fmt.Fprintf(&b, "    template IN %s %s {\n", set.Type, zone)
		fmt.Fprintf(&b, "        match ^%s$\n", regexp.QuoteMeta(set.FQDN))
		for _, value := range set.Values {
			fmt.Fprintf(&b, "        answer %q\n", fmt.Sprintf("{{ .Name }} %d IN %s %s", set.TTL, set.Type, templateValue(set.Type, value)))
		}
		b.WriteString("        fallthrough\n")
		b.WriteString("    }\n")
	}
	return b.String()
}

func templateValue(recordType dnsv1alpha1.DNSRecordType, value string) string {
	value = strings.TrimSpace(value)
	switch recordType {
	case dnsv1alpha1.DNSRecordTypeCNAME:
		return ensureTrailingDot(value)
	case dnsv1alpha1.DNSRecordTypeTXT:
		return strconv.Quote(value)
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
	if strings.HasSuffix(value, ".") {
		return value
	}
	return value + "."
}

func removeManagedBlock(corefile string) (string, error) {
	start := strings.Index(corefile, ManagedBlockStart)
	end := strings.Index(corefile, ManagedBlockEnd)
	if start == -1 && end == -1 {
		return corefile, nil
	}
	if start == -1 || end == -1 || end < start {
		return "", fmt.Errorf("managed Corefile block markers are incomplete or out of order")
	}
	end += len(ManagedBlockEnd)
	return corefile[:start] + corefile[end:], nil
}
