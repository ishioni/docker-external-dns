package registry

import (
	"strings"
	"testing"
)

func TestTXTKey(t *testing.T) {
	cases := []struct {
		recordType, dnsName, want string
	}{
		{"A", "foo.example.com", "a-foo.example.com"},
		{"a", "foo.example.com", "a-foo.example.com"},
		{"CNAME", "x.y.z", "cname-x.y.z"},
	}
	for _, c := range cases {
		if got := TXTKey(c.recordType, c.dnsName); got != c.want {
			t.Errorf("TXTKey(%q, %q) = %q, want %q", c.recordType, c.dnsName, got, c.want)
		}
	}
}

func TestEncodeTXT_IsQuoted(t *testing.T) {
	got := EncodeTXT("owner-1", "docker/whoami")
	if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
		t.Errorf("EncodeTXT must wrap value in double quotes for UniFi, got %q", got)
	}
	if !strings.Contains(got, "heritage=external-dns") {
		t.Errorf("encoded value missing heritage marker: %q", got)
	}
	if !strings.Contains(got, "external-dns/owner=owner-1") {
		t.Errorf("encoded value missing owner: %q", got)
	}
	if !strings.Contains(got, "external-dns/resource=docker/whoami") {
		t.Errorf("encoded value missing resource: %q", got)
	}
}

func TestDecodeTXT_RoundTrip(t *testing.T) {
	const owner = "docker-external-dns"
	const resource = "docker/whoami"

	rec, ok := DecodeTXT(EncodeTXT(owner, resource))
	if !ok {
		t.Fatalf("DecodeTXT(EncodeTXT(...)) failed")
	}
	if rec.OwnerID != owner {
		t.Errorf("OwnerID = %q, want %q", rec.OwnerID, owner)
	}
	if rec.Resource != resource {
		t.Errorf("Resource = %q, want %q", rec.Resource, resource)
	}
}

func TestDecodeTXT_StripsQuotes(t *testing.T) {
	// Even if the value comes back without quotes, it must still parse.
	unquoted := "heritage=external-dns,external-dns/owner=us,external-dns/resource=docker/x"
	if _, ok := DecodeTXT(unquoted); !ok {
		t.Errorf("DecodeTXT failed on unquoted value")
	}

	quoted := `"` + unquoted + `"`
	rec, ok := DecodeTXT(quoted)
	if !ok {
		t.Fatalf("DecodeTXT failed on quoted value")
	}
	if rec.OwnerID != "us" {
		t.Errorf("OwnerID = %q, want %q", rec.OwnerID, "us")
	}
}

func TestDecodeTXT_RejectsNonHeritage(t *testing.T) {
	cases := []string{
		"",
		"random text",
		"v=spf1 include:_spf.google.com ~all",
		`"some other quoted thing"`,
	}
	for _, c := range cases {
		if _, ok := DecodeTXT(c); ok {
			t.Errorf("DecodeTXT(%q) returned ok=true, want false", c)
		}
	}
}

func TestDecodeTXT_RequiresOwner(t *testing.T) {
	// Heritage present but no owner field — should reject.
	if _, ok := DecodeTXT("heritage=external-dns,external-dns/resource=foo"); ok {
		t.Error("DecodeTXT must reject values without an owner field")
	}
}

func TestIsOwnedBy(t *testing.T) {
	encoded := EncodeTXT("us", "docker/x")

	if !IsOwnedBy(encoded, "us") {
		t.Error("IsOwnedBy returned false for matching owner")
	}
	if IsOwnedBy(encoded, "them") {
		t.Error("IsOwnedBy returned true for non-matching owner")
	}
	if IsOwnedBy("not-an-ownership-record", "us") {
		t.Error("IsOwnedBy returned true for non-heritage value")
	}
}
