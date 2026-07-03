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
	ManagedBlockStart = "# BEGIN private-dns-zone-operator"
	ManagedBlockEnd   = "# END private-dns-zone-operator"
)

type ZoneDirective struct {
	Zone    string
	DBFile  string
	Policy  dnsv1alpha1.UnresolvedRecordPolicy
	Records []dnsrender.Record
}

func PatchCorefile(corefile string, directives []ZoneDirective) (string, error) {
	block := renderManagedBlock(directives)
	stripped, err := removeManagedBlock(corefile)
	if err != nil {
		return "", err
	}
	stripped = strings.TrimRight(stripped, "\n")
	return stripped + "\n\n" + block + "\n", nil
}

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
	sort.Slice(directives, func(i, j int) bool {
		return directives[i].Zone < directives[j].Zone
	})
	var b strings.Builder
	b.WriteString(ManagedBlockStart)
	for _, directive := range directives {
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

func renderTemplateRecords(records []dnsrender.Record) string {
	records = dnsrender.DeduplicateAndSort(records)
	var b strings.Builder
	for _, record := range records {
		fqdn := strings.TrimSpace(record.FQDN)
		if !strings.HasSuffix(fqdn, ".") {
			fqdn += "."
		}
		zone := strings.TrimSuffix(record.Zone, ".")
		fmt.Fprintf(&b, "    template IN %s %s {\n", record.Type, zone)
		fmt.Fprintf(&b, "        match ^%s$\n", regexp.QuoteMeta(fqdn))
		values := append([]string(nil), record.Values...)
		sort.Strings(values)
		for _, value := range values {
			fmt.Fprintf(&b, "        answer %q\n", fmt.Sprintf("{{ .Name }} %d IN %s %s", record.TTL, record.Type, templateValue(record.Type, value)))
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
