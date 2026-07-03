package dns

import (
	"strings"
	"testing"
	"time"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRenderZone(t *testing.T) {
	ttl := int32(300)
	zone := dnsv1alpha1.PrivateDNSZone{
		ObjectMeta: metav1.ObjectMeta{Name: "example"},
		Spec: dnsv1alpha1.PrivateDNSZoneSpec{
			Zone: "example.com",
			TTL:  &ttl,
		},
	}
	recordTTL := int32(60)
	zoneFile, err := RenderZone(zone, []Record{
		{Zone: "example.com.", Name: "api", FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: recordTTL, Values: []string{"10.0.0.2", "10.0.0.1"}},
		{Zone: "example.com.", Name: "info", FQDN: "info.example.com.", Type: dnsv1alpha1.DNSRecordTypeTXT, TTL: ttl, Values: []string{"hello"}},
	}, time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("render zone: %v", err)
	}
	for _, want := range []string{
		"$ORIGIN example.com.",
		"api 60 IN A 10.0.0.1",
		"api 60 IN A 10.0.0.2",
		"info 300 IN TXT \"hello\"",
	} {
		if !strings.Contains(zoneFile.Content, want) {
			t.Fatalf("expected rendered zone to contain %q:\n%s", want, zoneFile.Content)
		}
	}
}
