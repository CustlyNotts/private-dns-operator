package dns

import (
	"fmt"
	"strconv"
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
	zoneFile, err := RenderZone(testZone("example.com", nil), nil, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), PreviousRender{})
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
	first, err := RenderZone(testZone("example.com", nil), records, now, PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	// Same records in the opposite order must produce byte-identical output,
	// otherwise every reconcile would look like a change.
	second, err := RenderZone(testZone("example.com", nil), []Record{records[1], records[0]}, now, PreviousRender{})
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
	}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), PreviousRender{})
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
			}}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), PreviousRender{})
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

	if _, err := RenderZone(testZone("bad_zone", nil), nil, now, PreviousRender{}); err == nil {
		t.Error("expected an invalid zone suffix to be rejected")
	}
	if _, err := RenderZone(testZone("example.com", &zeroTTL), nil, now, PreviousRender{}); err == nil {
		t.Error("expected a zero zone TTL to be rejected")
	}
	_, err := RenderZone(testZone("example.com", nil), []Record{
		{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"nope"}},
	}, now, PreviousRender{})
	if err == nil {
		t.Error("expected an invalid record value to be rejected")
	}
}

func TestRenderZoneHonoursZoneTTL(t *testing.T) {
	ttl := int32(60)
	zoneFile, err := RenderZone(testZone("example.com", &ttl), nil, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC), PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if !strings.Contains(zoneFile.Content, "$TTL 60") {
		t.Errorf("zone TTL was not applied:\n%s", zoneFile.Content)
	}
}

// TestSerialAdvancesWhenRecordsChange is the regression test for NXDOMAIN zones
// silently serving stale records. CoreDNS's file plugin reloads a zone only when
// the SOA serial increases, and the serial used to come from a layout that
// resolved to YYYYMMDD plus the month repeated, so it only moved once a day.
// Every record change inside that window was invisible until a CoreDNS restart.
func TestSerialAdvancesWhenRecordsChange(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	one := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.1"}}}
	two := append(append([]Record(nil), one...), Record{
		Zone: "example.com.", FQDN: "web.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.2"},
	})

	first, err := RenderZone(testZone("example.com", nil), one, now, PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}

	// A change later the same day must still advance the serial.
	second, err := RenderZone(testZone("example.com", nil), two, now.Add(time.Minute),
		PreviousRender{Serial: first.Serial, Hash: first.Hash})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}

	if second.Serial == first.Serial {
		t.Fatalf("serial did not advance after a record change (%s); CoreDNS would refuse to reload", first.Serial)
	}
	if !serialGreater(t, second.Serial, first.Serial) {
		t.Errorf("serial went backwards: %s -> %s", first.Serial, second.Serial)
	}
	if !strings.Contains(second.Content, second.Serial+" ; serial") {
		t.Errorf("the chosen serial is not in the rendered SOA:\n%s", second.Content)
	}
}

// TestSerialIsStableWhenNothingChanges is the other half of the constraint. The
// serial sits inside the rendered content, so one that moved on every pass would
// change the content hash, write status, and retrigger the watch that scheduled
// the reconcile — the status loop this project already fixed once.
func TestSerialIsStableWhenNothingChanges(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	records := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.1"}}}

	first, err := RenderZone(testZone("example.com", nil), records, now, PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	// Much later, same records.
	second, err := RenderZone(testZone("example.com", nil), records, now.Add(72*time.Hour),
		PreviousRender{Serial: first.Serial, Hash: first.Hash})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}

	if second.Serial != first.Serial {
		t.Errorf("serial moved with no record change: %s -> %s", first.Serial, second.Serial)
	}
	if second.Content != first.Content {
		t.Errorf("unchanged records rendered different content:\n%s\n---\n%s", first.Content, second.Content)
	}
	if second.Hash != first.Hash {
		t.Errorf("hash moved with no record change: %s -> %s", first.Hash, second.Hash)
	}
}

func TestSerialSurvivesManyChangesInOneDay(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	previous := PreviousRender{}
	seen := map[string]struct{}{}
	last := ""

	// 150 changes in a day exceeds the two digits a YYYYMMDDnn serial has room
	// for, so this pins that it keeps advancing rather than wrapping or sticking.
	for i := 0; i < 150; i++ {
		records := []Record{{
			Zone: "example.com.", FQDN: "api.example.com.",
			Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60,
			Values: []string{fmt.Sprintf("10.0.0.%d", i+1)},
		}}
		zoneFile, err := RenderZone(testZone("example.com", nil), records, now, previous)
		if err != nil {
			t.Fatalf("render %d: %v", i, err)
		}
		if _, dup := seen[zoneFile.Serial]; dup {
			t.Fatalf("serial %s reused at change %d", zoneFile.Serial, i)
		}
		if last != "" && !serialGreater(t, zoneFile.Serial, last) {
			t.Fatalf("serial went backwards at change %d: %s -> %s", i, last, zoneFile.Serial)
		}
		seen[zoneFile.Serial] = struct{}{}
		last = zoneFile.Serial
		previous = PreviousRender{Serial: zoneFile.Serial, Hash: zoneFile.Hash}
	}
	if _, err := strconv.ParseUint(last, 10, 32); err != nil {
		t.Errorf("serial %s does not fit the 32 bits a SOA serial has: %v", last, err)
	}
}

// TestSerialAdvancesAcrossDays keeps the readable YYYYMMDDnn shape working.
func TestSerialAdvancesAcrossDays(t *testing.T) {
	day1 := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	records := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.1"}}}

	first, err := RenderZone(testZone("example.com", nil), records, day1, PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if first.Serial != "2026101401" {
		t.Errorf("first serial = %s, want the conventional 2026101401", first.Serial)
	}

	changed := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.2"}}}
	next, err := RenderZone(testZone("example.com", nil), changed, day1.Add(24*time.Hour),
		PreviousRender{Serial: first.Serial, Hash: first.Hash})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if next.Serial != "2026101501" {
		t.Errorf("next-day serial = %s, want 2026101501", next.Serial)
	}
}

// TestSerialAdvancesWhenTheClockGoesBackwards guards against a node with a bad
// clock stranding CoreDNS on stale records.
func TestSerialAdvancesWhenTheClockGoesBackwards(t *testing.T) {
	records := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.1"}}}
	changed := []Record{{Zone: "example.com.", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.0.0.2"}}}

	future, err := RenderZone(testZone("example.com", nil), records, time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC), PreviousRender{})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	past, err := RenderZone(testZone("example.com", nil), changed, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		PreviousRender{Serial: future.Serial, Hash: future.Hash})
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	if !serialGreater(t, past.Serial, future.Serial) {
		t.Errorf("a backwards clock produced a non-advancing serial: %s -> %s", future.Serial, past.Serial)
	}
}

func serialGreater(t *testing.T, a string, b string) bool {
	t.Helper()
	left, err := strconv.ParseUint(a, 10, 32)
	if err != nil {
		t.Fatalf("serial %q is not a 32-bit number: %v", a, err)
	}
	right, err := strconv.ParseUint(b, 10, 32)
	if err != nil {
		t.Fatalf("serial %q is not a 32-bit number: %v", b, err)
	}
	return left > right
}
