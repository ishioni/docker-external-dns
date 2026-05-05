// Package registry implements an external-dns-compatible TXT ownership registry.
// For every A record we manage at foo.example.com, we maintain a companion TXT
// record at a-foo.example.com with value:
//
//	heritage=external-dns,external-dns/owner=<ownerID>,external-dns/resource=<resource>
//
// This is wire-compatible with kubernetes-sigs/external-dns using --txt-prefix=%{record_type}-,
// so a Kubernetes external-dns instance with a different owner ID can coexist safely.
package registry

import (
	"fmt"
	"strings"
)

const heritage = "external-dns"

// OwnershipRecord holds the parsed payload of a TXT ownership record.
type OwnershipRecord struct {
	OwnerID  string
	Resource string
}

// TXTKey returns the TXT record name for the given DNS name and record type.
// e.g. TXTKey("A", "foo.example.com") == "a-foo.example.com"
func TXTKey(recordType, dnsName string) string {
	return fmt.Sprintf("%s-%s", strings.ToLower(recordType), dnsName)
}

// EncodeTXT produces the TXT record value for an ownership record.
// UniFi requires the value to be double-quoted when it contains commas.
func EncodeTXT(ownerID, resource string) string {
	return fmt.Sprintf(`"heritage=%s,external-dns/owner=%s,external-dns/resource=%s"`,
		heritage, ownerID, resource)
}

// DecodeTXT parses the TXT record value. Returns (record, true) if it is a
// valid external-dns ownership record, (zero, false) otherwise.
func DecodeTXT(value string) (OwnershipRecord, bool) {
	// Strip surrounding quotes that UniFi stores/returns.
	value = strings.Trim(value, `"`)

	if !strings.Contains(value, "heritage="+heritage) {
		return OwnershipRecord{}, false
	}

	var rec OwnershipRecord
	for _, part := range strings.Split(value, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "external-dns/owner":
			rec.OwnerID = v
		case "external-dns/resource":
			rec.Resource = v
		}
	}
	if rec.OwnerID == "" {
		return OwnershipRecord{}, false
	}
	return rec, true
}

// IsOwnedBy reports whether the TXT value was written by the given ownerID.
func IsOwnedBy(txtValue, ownerID string) bool {
	rec, ok := DecodeTXT(txtValue)
	return ok && rec.OwnerID == ownerID
}
