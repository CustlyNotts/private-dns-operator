package dns

import (
	"testing"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
)

func TestValidateRecordsAllowsMultipleARecords(t *testing.T) {
	records := []Record{
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}},
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.2"}},
	}
	if errs := ValidateRecords(records); len(errs) != 0 {
		t.Fatalf("expected records to be valid, got %v", errs)
	}
}

func TestValidateRecordsRejectsCNAMEWithA(t *testing.T) {
	records := []Record{
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.0.0.1"}},
		{FQDN: "api.example.com.", Type: dnsv1alpha1.DNSRecordTypeCNAME, TTL: 300, Values: []string{"target.example.com."}},
	}
	if errs := ValidateRecords(records); len(errs) == 0 {
		t.Fatal("expected CNAME conflict to be rejected")
	}
}
