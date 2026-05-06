// Package plan computes the minimal set of DNS changes needed to bring
// the current UniFi records into sync with the desired state derived from
// running containers.
package plan

import (
	"github.com/ishioni/docker-external-dns/internal/provider/unifi"
	"github.com/ishioni/docker-external-dns/internal/registry"
	"github.com/ishioni/docker-external-dns/internal/source"
)

// Changes holds the three buckets of DNS work to perform.
type Changes struct {
	// Create holds endpoints that don't yet have an A record in UniFi.
	Create []*source.Endpoint
	// Update holds endpoints whose A record exists but has a different target value.
	Update []*source.Endpoint
	// Delete holds A records owned by us that are no longer in the desired set.
	Delete []unifi.DNSRecord
}

// Compute diffs desired endpoints against the current UniFi records and
// returns what needs to change. It only touches records whose companion
// TXT ownership record matches ownerID — all others are left untouched.
// txtPrefix must match the value used when writing TXT keys (see registry.TXTKey).
func Compute(desired []*source.Endpoint, current []unifi.DNSRecord, ownerID, txtPrefix string) Changes {
	// Build lookup maps from current records.
	aByKey := make(map[string]unifi.DNSRecord) // key (hostname) → A record
	txtByKey := make(map[string]unifi.DNSRecord) // ownership TXT key → TXT record

	for _, r := range current {
		switch r.RecordType {
		case "A":
			aByKey[r.Key] = r
		case "TXT":
			txtByKey[r.Key] = r
		}
	}

	// Index our ownership: set of A-record hostnames we own.
	owned := make(map[string]bool)
	for _, txtRec := range txtByKey {
		if registry.IsOwnedBy(txtRec.Value, ownerID) {
			if hostname, ok := registry.HostnameFromTXTKey(txtPrefix, "A", txtRec.Key); ok {
				owned[hostname] = true
			}
		}
	}

	// Build desired set indexed by hostname.
	desiredByHost := make(map[string]*source.Endpoint, len(desired))
	for _, ep := range desired {
		desiredByHost[ep.DNSName] = ep
	}

	var changes Changes

	// Determine creates and updates.
	for _, ep := range desired {
		existing, exists := aByKey[ep.DNSName]
		if !exists {
			changes.Create = append(changes.Create, ep)
		} else if existing.Value != ep.Target {
			// Record exists but with wrong IP — update it only if we own it.
			if owned[ep.DNSName] {
				changes.Update = append(changes.Update, ep)
			}
		}
	}

	// Determine deletes: owned records whose hostname is no longer desired.
	for hostname := range owned {
		if _, wanted := desiredByHost[hostname]; !wanted {
			if aRec, ok := aByKey[hostname]; ok {
				changes.Delete = append(changes.Delete, aRec)
			}
		}
	}

	return changes
}
