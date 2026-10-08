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
	start := strings.Index(got, ManagedBlockStart)
	if start < 0 {
		t.Fatalf("managed block marker is missing:\n%s", got)
	}
	managed := got[start:]
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

func TestPatchCorefileLeavesUnmanagedConfigUntouched(t *testing.T) {
	corefile := `.:53 {
    errors
    health
    kubernetes cluster.local in-addr.arpa ip6.arpa {
       pods insecure
    }
    forward . /etc/resolv.conf
    reload
}
corp.example:53 {
    forward . 10.0.0.53
}`
	got, err := PatchCorefile(corefile, []ZoneDirective{{
		Zone: "new.example.", DBFile: "new-example-abc123.db", Policy: dnsv1alpha1.UnresolvedRecordPolicyNXDOMAIN,
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	// A hand-maintained server block outside the markers must survive verbatim.
	if !strings.Contains(got, "corp.example:53 {\n    forward . 10.0.0.53\n}") {
		t.Fatalf("unmanaged server block was modified:\n%s", got)
	}
	if !strings.Contains(got, "pods insecure") {
		t.Fatalf("unmanaged kubernetes plugin config was modified:\n%s", got)
	}
}

func TestExtractZoneBlocks(t *testing.T) {
	corefile := `.:53 {
    errors
    reload
}

# BEGIN private-dns-zone-operator

alpha.internal:53 {
    errors
    file /etc/coredns/alpha-internal-a1b2c3.db alpha.internal
    cache 30
}

beta.internal:53 {
    errors
    template IN A beta.internal {
        match ^api\.beta\.internal\.$
        answer "{{ .Name }} 60 IN A 10.0.0.1"
        fallthrough
    }
    forward . /etc/resolv.conf
    cache 30
}
# END private-dns-zone-operator`

	blocks := ExtractZoneBlocks(corefile)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 zone blocks, got %d: %v", len(blocks), blocks)
	}
	if !strings.HasPrefix(blocks["alpha.internal"], "alpha.internal:53 {") {
		t.Errorf("alpha block = %q", blocks["alpha.internal"])
	}
	if !strings.Contains(blocks["beta.internal"], `answer "{{ .Name }} 60 IN A 10.0.0.1"`) {
		t.Errorf("beta block lost its template record: %q", blocks["beta.internal"])
	}
	// The nested template stanza closes with an indented brace, so the scanner
	// must not treat it as the end of the server block.
	if !strings.HasSuffix(blocks["beta.internal"], "\n}") {
		t.Errorf("beta block did not capture through its closing brace: %q", blocks["beta.internal"])
	}
}

func TestExtractZoneBlocksWithoutManagedBlock(t *testing.T) {
	if blocks := ExtractZoneBlocks(".:53 {\n    reload\n}"); len(blocks) != 0 {
		t.Fatalf("expected no zone blocks, got %v", blocks)
	}
}

func TestPatchCorefileCarriesRawDirectiveForward(t *testing.T) {
	previous := "held.example:53 {\n    errors\n    file /etc/coredns/held-example-a1b2c3.db held.example\n    cache 30\n}"
	got, err := PatchCorefile("", []ZoneDirective{{Zone: "held.example.", Raw: previous}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	if !strings.Contains(got, previous) {
		t.Fatalf("raw directive was not carried forward verbatim:\n%s", got)
	}
}

func TestRemoveManagedBlockRejectsBrokenMarkers(t *testing.T) {
	// End before start, which would otherwise splice the file incorrectly.
	_, err := PatchCorefile(ManagedBlockEnd+"\n"+ManagedBlockStart, nil)
	if err == nil {
		t.Fatal("expected out-of-order markers to be rejected")
	}
}

func TestHasReloadPlugin(t *testing.T) {
	tests := []struct {
		name     string
		corefile string
		want     bool
	}{
		{name: "bare reload", corefile: ".:53 {\n    reload\n}", want: true},
		{name: "reload with interval", corefile: ".:53 {\n    reload 10s\n}", want: true},
		{name: "absent", corefile: ".:53 {\n    errors\n}", want: false},
		{name: "not a prefix match", corefile: ".:53 {\n    reloadsomething\n}", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := HasReloadPlugin(test.corefile); got != test.want {
				t.Errorf("HasReloadPlugin() = %v, want %v", got, test.want)
			}
		})
	}
}

// TestForwardModeMergesRecordsAtTheSameName is the regression test for records
// being silently dropped in Forward mode. Two PrivateDNSRecord objects publishing
// different addresses at one name previously rendered as two template stanzas
// with identical match expressions. CoreDNS answers from the first stanza that
// matches and returns, so only one of the two addresses was ever served.
func TestForwardModeMergesRecordsAtTheSameName(t *testing.T) {
	got, err := PatchCorefile("", []ZoneDirective{{
		Zone:   "platform.internal.",
		Policy: dnsv1alpha1.UnresolvedRecordPolicyForward,
		Records: []dnsrender.Record{
			{
				Zone: "platform.internal.", FQDN: "api.platform.internal.",
				Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.9.9.1"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "one"},
			},
			{
				Zone: "platform.internal.", FQDN: "api.platform.internal.",
				Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.9.9.2"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Namespace: "default", Name: "two"},
			},
		},
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}

	if n := strings.Count(got, "template IN A platform.internal {"); n != 1 {
		t.Errorf("got %d A template stanzas, want exactly 1 so CoreDNS serves both answers:\n%s", n, got)
	}
	for _, want := range []string{
		`answer "{{ .Name }} 60 IN A 10.9.9.1"`,
		`answer "{{ .Name }} 60 IN A 10.9.9.2"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Corefile is missing %s:\n%s", want, got)
		}
	}
	if n := strings.Count(got, `match ^api\.platform\.internal\.$`); n != 1 {
		t.Errorf("the same name is matched by %d stanzas, want 1:\n%s", n, got)
	}
}

func TestForwardModeKeepsDifferentTypesSeparate(t *testing.T) {
	got, err := PatchCorefile("", []ZoneDirective{{
		Zone:   "platform.internal.",
		Policy: dnsv1alpha1.UnresolvedRecordPolicyForward,
		Records: []dnsrender.Record{
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.9.9.1"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "a"}},
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeTXT, TTL: 60, Values: []string{"hello"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "txt"}},
		},
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	// A and TXT at the same name are distinct RRsets and need their own stanzas.
	if !strings.Contains(got, "template IN A platform.internal {") {
		t.Errorf("missing A stanza:\n%s", got)
	}
	if !strings.Contains(got, "template IN TXT platform.internal {") {
		t.Errorf("missing TXT stanza:\n%s", got)
	}
}

func TestForwardModeUsesTheLowestTTLInAnRRSet(t *testing.T) {
	got, err := PatchCorefile("", []ZoneDirective{{
		Zone:   "platform.internal.",
		Policy: dnsv1alpha1.UnresolvedRecordPolicyForward,
		Records: []dnsrender.Record{
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 300, Values: []string{"10.9.9.1"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "slow"}},
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 30, Values: []string{"10.9.9.2"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "fast"}},
		},
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	// RFC 2181 section 5.2: one TTL for the whole RRset, lowest wins.
	if strings.Contains(got, "300 IN A") {
		t.Errorf("mixed TTLs should collapse to the lowest, got:\n%s", got)
	}
	if n := strings.Count(got, "30 IN A"); n != 2 {
		t.Errorf("expected both answers at TTL 30, got %d:\n%s", n, got)
	}
}

func TestForwardModeDeduplicatesIdenticalValuesAcrossRecords(t *testing.T) {
	got, err := PatchCorefile("", []ZoneDirective{{
		Zone:   "platform.internal.",
		Policy: dnsv1alpha1.UnresolvedRecordPolicyForward,
		Records: []dnsrender.Record{
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.9.9.1"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "one"}},
			{Zone: "platform.internal.", FQDN: "api.platform.internal.", Type: dnsv1alpha1.DNSRecordTypeA, TTL: 60, Values: []string{"10.9.9.1"},
				Source: dnsrender.Source{Kind: "PrivateDNSRecord", Name: "duplicate"}},
		},
	}})
	if err != nil {
		t.Fatalf("patch corefile: %v", err)
	}
	if n := strings.Count(got, `10.9.9.1"`); n != 1 {
		t.Errorf("two records with the same value should answer once, got %d:\n%s", n, got)
	}
}
