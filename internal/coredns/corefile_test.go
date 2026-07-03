package coredns

import (
	"strings"
	"testing"

	dnsv1alpha1 "github.com/custlynotts/private-dns-operator/api/v1alpha1"
	dnsrender "github.com/custlynotts/private-dns-operator/internal/dns"
)

func TestPatchCorefileAppendsDedicatedForwardServerBlock(t *testing.T) {
	corefile := `.:53 {
    errors
    forward . /etc/resolv.conf
    reload
}`
	got, err := PatchCorefile(corefile, []ZoneDirective{{
		Zone: "rancher.io.", DBFile: "rancher-io-abc123.db", Policy: dnsv1alpha1.UnresolvedRecordPolicyForward,
		Records: []dnsrender.Record{{
			Zone: "rancher.io.", FQDN: "git.rancher.io.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"34.208.213.149", "34.208.213.150"},
		}},
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	for _, want := range []string{
		ManagedBlockStart,
		"rancher.io:53 {",
		"template IN A rancher.io {",
		"match ^git\\.rancher\\.io\\.$",
		`answer "{{ .Name }} 60 IN A 34.208.213.149"`,
		`answer "{{ .Name }} 60 IN A 34.208.213.150"`,
		"fallthrough",
		"forward . /etc/resolv.conf",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected rendered Corefile to contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "file /etc/coredns/rancher-io-abc123.db") {
		t.Fatalf("Forward policy must not use the authoritative file plugin:\n%s", got)
	}
}

func TestPatchCorefileRendersAuthoritativeNXDOMAINServerBlock(t *testing.T) {
	corefile := `.:53 {
    errors
    forward . /etc/resolv.conf
}`
	got, err := PatchCorefile(corefile, []ZoneDirective{{
		Zone: "new.example.", DBFile: "new-example-abc123.db", Policy: dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN,
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	if !strings.Contains(got, "file /etc/coredns/new-example-abc123.db new.example") {
		t.Fatalf("expected NXDOMAIN policy to render file plugin:\n%s", got)
	}
	managed := got[strings.Index(got, ManagedBlockStart):]
	if strings.Contains(managed, "forward . /etc/resolv.conf") || strings.Contains(managed, "template IN") {
		t.Fatalf("NXDOMAIN policy should be authoritative without template/forward fallback:\n%s", managed)
	}
}

func TestPatchCorefileReplacesManagedBlock(t *testing.T) {
	corefile := `.:53 {
    errors
    forward . /etc/resolv.conf
}
# BEGIN private-dns-zone-operator
old.example:53 {
    file /etc/coredns/old.db old.example
}
# END private-dns-zone-operator`
	got, err := PatchCorefile(corefile, []ZoneDirective{{
		Zone: "new.example.", DBFile: "new-example-abc123.db", Policy: dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN,
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	if strings.Contains(got, "old.db") || !strings.Contains(got, "new-example-abc123.db") {
		t.Fatalf("managed block was not replaced:\n%s", got)
	}
}
