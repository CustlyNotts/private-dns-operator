package dns

import (
	"errors"
	"strings"
	"testing"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

func TestNormalizeZone(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "adds trailing dot", in: "example.com", want: "example.com."},
		{name: "keeps trailing dot", in: "example.com.", want: "example.com."},
		{name: "lowercases", in: "Example.COM", want: "example.com."},
		{name: "trims whitespace", in: "  example.com  ", want: "example.com."},
		{name: "empty", in: "", wantErr: true},
		{name: "root", in: ".", wantErr: true},
		{name: "empty label", in: "example..com", wantErr: true},
		{name: "leading hyphen label", in: "-bad.example.com", wantErr: true},
		{name: "trailing hyphen label", in: "bad-.example.com", wantErr: true},
		{name: "invalid character", in: "under_score.example.com", wantErr: true},
		{name: "label too long", in: strings.Repeat("a", 64) + ".example.com", wantErr: true},
		{name: "domain too long", in: strings.TrimSuffix(strings.Repeat("abcdefghij.", 24), ".") + ".example.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeZone(test.in)
			if test.wantErr {
				if err == nil {
					t.Fatalf("NormalizeZone(%q) = %q, want an error", test.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeZone(%q): %v", test.in, err)
			}
			if got != test.want {
				t.Errorf("NormalizeZone(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}

func TestFQDN(t *testing.T) {
	tests := []struct {
		name    string
		record  string
		zone    string
		want    string
		wantErr bool
	}{
		{name: "relative name", record: "api", zone: "example.com", want: "api.example.com."},
		{name: "multi label relative name", record: "api.dev", zone: "example.com", want: "api.dev.example.com."},
		{name: "apex via at", record: "@", zone: "example.com", want: "example.com."},
		{name: "apex via empty", record: "", zone: "example.com", want: "example.com."},
		{name: "absolute inside zone", record: "api.example.com.", zone: "example.com", want: "api.example.com."},
		{name: "absolute equal to zone", record: "example.com.", zone: "example.com", want: "example.com."},
		{name: "absolute outside zone", record: "api.other.com.", zone: "example.com", wantErr: true},
		{name: "invalid zone", record: "api", zone: "", wantErr: true},
		{name: "invalid relative name", record: "bad_name", zone: "example.com", wantErr: true},
		{name: "invalid absolute name", record: "bad_name.example.com.", zone: "example.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := FQDN(test.record, test.zone)
			if test.wantErr {
				if err == nil {
					t.Fatalf("FQDN(%q, %q) = %q, want an error", test.record, test.zone, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("FQDN(%q, %q): %v", test.record, test.zone, err)
			}
			if got != test.want {
				t.Errorf("FQDN(%q, %q) = %q, want %q", test.record, test.zone, got, test.want)
			}
		})
	}
}

func TestValidateRecordValues(t *testing.T) {
	tests := []struct {
		name       string
		recordType dnsv1alpha1.DNSRecordType
		value      string
		wantErr    bool
	}{
		{name: "A accepts IPv4", recordType: dnsv1alpha1.DNSRecordTypeA, value: "10.0.0.1"},
		{name: "A rejects IPv6", recordType: dnsv1alpha1.DNSRecordTypeA, value: "2001:db8::1", wantErr: true},
		{name: "A rejects hostname", recordType: dnsv1alpha1.DNSRecordTypeA, value: "host.example.com", wantErr: true},
		{name: "AAAA accepts IPv6", recordType: dnsv1alpha1.DNSRecordTypeAAAA, value: "2001:db8::1"},
		{name: "AAAA rejects IPv4", recordType: dnsv1alpha1.DNSRecordTypeAAAA, value: "10.0.0.1", wantErr: true},
		{name: "CNAME accepts hostname", recordType: dnsv1alpha1.DNSRecordTypeCNAME, value: "target.example.com"},
		{name: "CNAME rejects invalid hostname", recordType: dnsv1alpha1.DNSRecordTypeCNAME, value: "bad_target", wantErr: true},
		{name: "TXT accepts text", recordType: dnsv1alpha1.DNSRecordTypeTXT, value: "any text at all"},
		{name: "TXT rejects empty", recordType: dnsv1alpha1.DNSRecordTypeTXT, value: "", wantErr: true},
		{name: "MX accepts priority and host", recordType: dnsv1alpha1.DNSRecordTypeMX, value: "10 mail.example.com"},
		{name: "MX rejects missing host", recordType: dnsv1alpha1.DNSRecordTypeMX, value: "10", wantErr: true},
		{name: "MX rejects non numeric priority", recordType: dnsv1alpha1.DNSRecordTypeMX, value: "high mail.example.com", wantErr: true},
		{name: "MX rejects invalid host", recordType: dnsv1alpha1.DNSRecordTypeMX, value: "10 bad_host", wantErr: true},
		{name: "SRV accepts four fields", recordType: dnsv1alpha1.DNSRecordTypeSRV, value: "10 5 443 svc.example.com"},
		{name: "SRV rejects three fields", recordType: dnsv1alpha1.DNSRecordTypeSRV, value: "10 5 443", wantErr: true},
		{name: "SRV rejects non numeric port", recordType: dnsv1alpha1.DNSRecordTypeSRV, value: "10 5 https svc.example.com", wantErr: true},
		{name: "unknown type", recordType: dnsv1alpha1.DNSRecordType("NAPTR"), value: "anything", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records := []Record{{
				FQDN: "api.example.com.", Type: test.recordType, TTL: 300, Values: []string{test.value},
			}}
			errs := ValidateRecords(records)
			if test.wantErr && len(errs) == 0 {
				t.Fatalf("expected %s value %q to be rejected", test.recordType, test.value)
			}
			if !test.wantErr && len(errs) != 0 {
				t.Fatalf("expected %s value %q to be accepted, got %v", test.recordType, test.value, errs)
			}
		})
	}
}

func TestValidateRecordsRejectsNonPositiveTTL(t *testing.T) {
	errs := ValidateRecords([]Record{{
		FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 0, Values: []string{"10.0.0.1"},
	}})
	if len(errs) == 0 {
		t.Fatal("expected a zero TTL to be rejected")
	}
}

func TestValidateRecordsRejectsEmptyValues(t *testing.T) {
	errs := ValidateRecords([]Record{{
		FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300,
	}})
	if len(errs) == 0 {
		t.Fatal("expected a record with no values to be rejected")
	}
}

func TestValidateRecordsAllowsSingleCNAME(t *testing.T) {
	errs := ValidateRecords([]Record{{
		FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeCNAME, TTL: 300, Values: []string{"target.example.com"},
	}})
	if len(errs) != 0 {
		t.Fatalf("a lone CNAME should be valid, got %v", errs)
	}
}

func TestValidateRecordsRejectsMultipleCNAMEValues(t *testing.T) {
	errs := ValidateRecords([]Record{{
		FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeCNAME, TTL: 300,
		Values: []string{"one.example.com", "two.example.com"},
	}})
	if len(errs) == 0 {
		t.Fatal("a CNAME with two targets is ambiguous and should be rejected")
	}
}

func TestValidateRecordsAllowsSameNameDifferentTypes(t *testing.T) {
	errs := ValidateRecords([]Record{
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}},
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeTXT, TTL: 300, Values: []string{"hello"}},
	})
	if len(errs) != 0 {
		t.Fatalf("A and TXT at the same name are compatible, got %v", errs)
	}
}

func TestValidationErrorMessage(t *testing.T) {
	namespaced := ValidationError{
		Source:  Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "api"},
		FQDN:    "api.example.com.",
		Message: "boom",
	}
	if got := namespaced.Error(); !strings.Contains(got, "default/api") {
		t.Errorf("namespaced error should name the namespace and object, got %q", got)
	}

	clusterScoped := ValidationError{
		Source:  Source{Kind: "PrivateDNSZone", Name: "example"},
		FQDN:    "api.example.com.",
		Message: "boom",
	}
	if got := clusterScoped.Error(); strings.Contains(got, "/api") {
		t.Errorf("cluster-scoped error should not carry a namespace prefix, got %q", got)
	}
}

func TestDeduplicateAndSort(t *testing.T) {
	records := DeduplicateAndSort([]Record{
		{FQDN: "b.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.2", "10.0.0.1", "10.0.0.1"}},
		{FQDN: "a.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.3"}},
	})
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].FQDN != "a.example.com." {
		t.Errorf("records should be sorted by FQDN, got %q first", records[0].FQDN)
	}
	if got := records[1].Values; len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "10.0.0.2" {
		t.Errorf("values should be deduplicated and sorted, got %v", got)
	}
}

func TestDeduplicateAndSortCollapsesIdenticalRecords(t *testing.T) {
	source := Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "api"}
	records := DeduplicateAndSort([]Record{
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}, Source: source},
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}, Source: source},
	})
	if len(records) != 1 {
		t.Fatalf("records from the same source with the same data should collapse, got %d", len(records))
	}
}

func TestZoneFileKeyIsStableAndZoneSpecific(t *testing.T) {
	first := ZoneFileKey("example.com")
	if first != ZoneFileKey("Example.COM.") {
		t.Error("the key should not depend on case or a trailing dot")
	}
	if !strings.HasPrefix(first, "example-com-") || !strings.HasSuffix(first, ".db") {
		t.Errorf("unexpected key shape: %q", first)
	}
	if first == ZoneFileKey("example.net") {
		t.Error("different zones must not share a key")
	}
	// An unnormalizable zone still has to produce a usable key, because the
	// controller derives one before it knows whether the zone is valid.
	if got := ZoneFileKey("not a zone!"); got == "" {
		t.Error("expected a fallback key for an invalid zone")
	}
}

func cnameRecord(sourceName string, values ...string) Record {
	return Record{
		FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeCNAME, TTL: 300, Values: values,
		Source: Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: sourceName},
	}
}

// TestValidateRecordSetCNAMERules pins which CNAME situations are a genuine
// conflict. The rule is about distinct targets and coexisting types, not about
// how many objects happen to declare the same thing: duplicates that agree
// describe one RRset and are as compatible here as they are for an A record.
func TestValidateRecordSetCNAMERules(t *testing.T) {
	tests := []struct {
		name    string
		records []Record
		wantErr bool
	}{
		{
			name:    "a lone CNAME is valid",
			records: []Record{cnameRecord("one", "target.example.com")},
		},
		{
			name: "two records naming the same target are a compatible duplicate",
			records: []Record{
				cnameRecord("one", "target.example.com"),
				cnameRecord("two", "target.example.com"),
			},
		},
		{
			name:    "the same target listed twice on one record is a compatible duplicate",
			records: []Record{cnameRecord("one", "target.example.com", "target.example.com")},
		},
		{
			name:    "a trailing dot does not make a second target",
			records: []Record{cnameRecord("one", "target.example.com", "target.example.com.")},
		},
		{
			name:    "case does not make a second target",
			records: []Record{cnameRecord("one", "Target.Example.COM", "target.example.com")},
		},
		{
			name: "two different targets are ambiguous",
			records: []Record{
				cnameRecord("one", "a.example.com"),
				cnameRecord("two", "b.example.com"),
			},
			wantErr: true,
		},
		{
			name:    "two different targets on one record are ambiguous",
			records: []Record{cnameRecord("one", "a.example.com", "b.example.com")},
			wantErr: true,
		},
		{
			name: "a CNAME cannot share a name with an A record",
			records: []Record{
				cnameRecord("cname", "target.example.com"),
				{FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"},
					Source: Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "a"}},
			},
			wantErr: true,
		},
		{
			name: "a CNAME cannot share a name with a TXT record",
			records: []Record{
				cnameRecord("cname", "target.example.com"),
				{FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeTXT, TTL: 300, Values: []string{"hello"},
					Source: Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "txt"}},
			},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			errs := ValidateRecordSet(test.records)
			if test.wantErr && len(errs) == 0 {
				t.Fatal("expected this record set to be rejected")
			}
			if !test.wantErr && len(errs) != 0 {
				t.Fatalf("expected this record set to be accepted, got %v", errs)
			}
		})
	}
}

// TestValidateRecordSetBlamesTheCNAME checks the error points at the record whose
// constraint is broken, so the offending object is the one marked rejected.
func TestValidateRecordSetBlamesTheCNAME(t *testing.T) {
	errs := ValidateRecordSet([]Record{
		{FQDN: "alias.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"},
			Source: Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "the-a-record"}},
		cnameRecord("the-cname", "target.example.com"),
	})
	if len(errs) != 1 {
		t.Fatalf("expected one error, got %v", errs)
	}
	var validationErr ValidationError
	if !errors.As(errs[0], &validationErr) {
		t.Fatalf("expected a ValidationError, got %T", errs[0])
	}
	if validationErr.Source.Name != "the-cname" {
		t.Errorf("blamed %q, want the CNAME record", validationErr.Source.Name)
	}
}
