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

const (
	heritage         = "external-dns"
	wildcardTXTLabel = "wildcard-dexd"
	wildcardDNSLabel = "*"
)

// OwnershipRecord holds the parsed payload of a TXT ownership record.
type OwnershipRecord struct {
	OwnerID  string
	Resource string
}

// TXTKey returns the TXT record name for the given DNS name and record type.
// An optional prefix is prepended verbatim (include a trailing separator if needed).
// e.g. TXTKey("", "A", "foo.example.com") == "a-foo.example.com"
// e.g. TXTKey("talos.", "A", "foo.example.com") == "talos.a-foo.example.com"
func TXTKey(prefix, recordType, dnsName string) string {
	return prefix + strings.ToLower(recordType) + "-" + encodeTXTKeyHostname(dnsName)
}

// HostnameFromTXTKey reverses TXTKey: given the same prefix and record type, it
// strips the leading "prefix+recordType-" from txtKey and returns the original
// hostname. Returns ("", false) if txtKey does not carry the expected prefix.
func HostnameFromTXTKey(prefix, recordType, txtKey string) (string, bool) {
	expected := prefix + strings.ToLower(recordType) + "-"
	hostname, ok := strings.CutPrefix(txtKey, expected)
	if !ok {
		return "", false
	}
	return decodeTXTKeyHostname(hostname), true
}

// ParseTXTKey strips prefix from txtKey, then splits on the first '-' to
// recover the upper-case record type and hostname.
func ParseTXTKey(prefix, txtKey string) (recordType, hostname string, ok bool) {
	rest, ok := strings.CutPrefix(txtKey, prefix)
	if !ok {
		return "", "", false
	}
	recordType, hostname, ok = strings.Cut(rest, "-")
	if !ok || recordType == "" || hostname == "" {
		return "", "", false
	}
	return strings.ToUpper(recordType), decodeTXTKeyHostname(hostname), true
}

func encodeTXTKeyHostname(hostname string) string {
	labels := strings.Split(hostname, ".")
	for i, label := range labels {
		if label == wildcardDNSLabel {
			labels[i] = wildcardTXTLabel
		}
	}
	return strings.Join(labels, ".")
}

func decodeTXTKeyHostname(hostname string) string {
	labels := strings.Split(hostname, ".")
	for i, label := range labels {
		if label == wildcardTXTLabel {
			labels[i] = wildcardDNSLabel
		}
	}
	return strings.Join(labels, ".")
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
