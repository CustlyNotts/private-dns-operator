package dns

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

func testZone(suffix string, ttl *int32) dnsv1alpha1.PrivateDNSZone {
	return dnsv1alpha1.PrivateDNSZone{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec:       dnsv1alpha1.PrivateDNSZoneSpec{Zone: suffix, TTL: ttl},
	}
}

func TestRenderZoneEmitsSOAAndNS(t *testing.T) {
	zoneFile, err := RenderZone(testZone("example.com", nil), nil, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	for _, want := range []string{
		"$ORIGIN example.com.",
		"$TTL 300",
		"@ IN SOA ns.dns.example.com. hostmaster.example.com. (",
		"@ IN NS ns.dns.example.com.",
		"; serial",
	} {
		if !strings.Contains(zoneFile.Content, want) {
			t.Errorf("zone file missing %q:\n%s", want, zoneFile.Content)
		}
	}
	if zoneFile.Serial == "" || zoneFile.Hash == "" || zoneFile.Key == "" {
		t.Errorf("zone file metadata incomplete: %+v", zoneFile)
	}
}

func TestRenderZoneIsDeterministic(t *testing.T) {
	now := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	records := []Record{
		{Zone: "example.com.", FQDN: "b.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.2"}},
		{Zone: "example.com.", FQDN: "a.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}},
	}
	first, err := RenderZone(testZone("example.com", nil), records, now)
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	// Same records in the opposite order must produce byte-identical output,
	// otherwise every reconcile would look like a change.
	second, err := RenderZone(testZone("example.com", nil), []Record{records[1], records[0]}, now)
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if first.Content != second.Content || first.Hash != second.Hash {
		t.Errorf("rendering is order dependent:\n%s\n---\n%s", first.Content, second.Content)
	}
}

func TestRenderZoneRendersApexAsAt(t *testing.T) {
	zoneFile, err := RenderZone(testZone("example.com", nil), []Record{
		{Zone: "example.com.", FQDN: "example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}},
	}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if !strings.Contains(zoneFile.Content, "@ 300 IN A 10.0.0.1") {
		t.Errorf("apex record should render as @:\n%s", zoneFile.Content)
	}
}

func TestRenderZoneValueFormatting(t *testing.T) {
	tests := []struct {
		name       string
		recordType dnsv1alpha1.DNSRecordType
		value      string
		want       string
	}{
		{name: "CNAME gains a trailing dot", recordType: dnsv1alpha1.DNSRecordTypeCNAME, value: "target.example.net", want: "IN CNAME target.example.net."},
		{name: "CNAME keeps its trailing dot", recordType: dnsv1alpha1.DNSRecordTypeCNAME, value: "target.example.net.", want: "IN CNAME target.example.net."},
		{name: "TXT is quoted", recordType: dnsv1alpha1.DNSRecordTypeTXT, value: "hello world", want: `IN TXT "hello world"`},
		{name: "MX host gains a trailing dot", recordType: dnsv1alpha1.DNSRecordTypeMX, value: "10 mail.example.net", want: "IN MX 10 mail.example.net."},
		{name: "SRV target gains a trailing dot", recordType: dnsv1alpha1.DNSRecordTypeSRV, value: "10 5 443 svc.example.net", want: "IN SRV 10 5 443 svc.example.net."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zoneFile, err := RenderZone(testZone("example.com", nil), []Record{{
				Zone: "example.com.", FQDN: "api.example.com.", Type: test.recordType, TTL: 300, Values: []string{test.value},
			}}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("render zone: %v", err)
			}
			if !strings.Contains(zoneFile.Content, test.want) {
				t.Errorf("zone file missing %q:\n%s", test.want, zoneFile.Content)
			}
		})
	}
}

func TestRenderZoneRejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
	zeroTTL := int32(0)

	if _, err := RenderZone(testZone("bad_zone", nil), nil, now); err == nil {
		t.Error("expected an invalid zone suffix to be rejected")
	}
	if _, err := RenderZone(testZone("example.com", &zeroTTL), nil, now); err == nil {
		t.Error("expected a zero zone TTL to be rejected")
	}
	_, err := RenderZone(testZone("example.com", nil), []Record{
		{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"nope"}},
	}, now)
	if err == nil {
		t.Error("expected an invalid record value to be rejected")
	}
}

func TestRenderZoneHonoursZoneTTL(t *testing.T) {
	ttl := int32(60)
	zoneFile, err := RenderZone(testZone("example.com", &ttl), nil, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if !strings.Contains(zoneFile.Content, "$TTL 60") {
		t.Errorf("zone TTL was not applied:\n%s", zoneFile.Content)
	}
}
