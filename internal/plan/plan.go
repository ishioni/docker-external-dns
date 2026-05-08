// Package plan computes the minimal set of DNS changes needed to bring
// the current UniFi records into sync with the desired state derived from
// running containers.
package plan

import (
	"log/slog"

	"github.com/ishioni/docker-external-dns/internal/provider/unifi"
	"github.com/ishioni/docker-external-dns/internal/registry"
	"github.com/ishioni/docker-external-dns/internal/source"
)

// Changes holds the buckets of DNS work to perform.
type Changes struct {
	// Create holds endpoints that don't yet have a matching record in UniFi.
	Create []*source.Endpoint
	// Update holds endpoints whose record exists but has a different target value.
	Update []*source.Endpoint
	// Delete holds A/CNAME records owned by us that are no longer in the desired set.
	Delete []unifi.DNSRecord
	// OrphanTXT holds TXT ownership records we own that have no companion A/CNAME
	// and are no longer desired. They should be deleted to keep UniFi clean.
	OrphanTXT []unifi.DNSRecord
}

type recordKey struct {
	Hostname   string
	RecordType string
}

// Compute diffs desired endpoints against the current UniFi records and
// returns what needs to change. It only touches records whose companion
// TXT ownership record matches ownerID — all others are left untouched.
// txtPrefix must match the value used when writing TXT keys (see registry.TXTKey).
func Compute(desired []*source.Endpoint, current []unifi.DNSRecord, ownerID, txtPrefix string) Changes {
	// Build lookup maps from current records.
	aOrCnameByKey := make(map[recordKey]unifi.DNSRecord)
	txtByKey := make(map[string]unifi.DNSRecord) // ownership TXT key → TXT record

	for _, r := range current {
		switch r.RecordType {
		case "A", "CNAME":
			aOrCnameByKey[recordKey{Hostname: r.Key, RecordType: r.RecordType}] = r
		case "TXT":
			txtByKey[r.Key] = r
		}
	}

	// Index our ownership: set of record keys we own.
	owned := make(map[recordKey]bool)
	for _, txtRec := range txtByKey {
		if registry.IsOwnedBy(txtRec.Value, ownerID) {
			recordType, hostname, ok := registry.ParseTXTKey(txtPrefix, txtRec.Key)
			if ok {
				owned[recordKey{Hostname: hostname, RecordType: recordType}] = true
			}
		}
	}

	// Build desired set; detect collisions (same hostname+type from two containers).
	// Upstream callers sort containers by name, so "first" is deterministic.
	desiredByKey := make(map[recordKey]*source.Endpoint, len(desired))
	for _, ep := range desired {
		key := recordKey{Hostname: ep.DNSName, RecordType: ep.RecordType}
		if existing, collision := desiredByKey[key]; collision {
			slog.Warn("hostname collision: two containers claim the same record; keeping first",
				"hostname", ep.DNSName,
				"record_type", ep.RecordType,
				"winner", existing.Resource,
				"dropped", ep.Resource,
			)
			continue
		}
		desiredByKey[key] = ep
	}

	var changes Changes

	// Determine creates and updates.
	for key, ep := range desiredByKey {
		existing, exists := aOrCnameByKey[key]
		if !exists {
			changes.Create = append(changes.Create, ep)
		} else if !owned[key] {
			// Record exists in UniFi but we did not create it — warn every reconcile.
			slog.Warn("unowned record at desired hostname; skipping until manually resolved",
				"hostname", key.Hostname,
				"record_type", key.RecordType,
				"current_value", existing.Value,
				"desired_value", ep.Target,
			)
		} else if existing.Value != ep.Target {
			changes.Update = append(changes.Update, ep)
		}
	}

	// Determine deletes: owned records whose key is no longer desired.
	for key := range owned {
		if _, wanted := desiredByKey[key]; !wanted {
			if rec, ok := aOrCnameByKey[key]; ok {
				changes.Delete = append(changes.Delete, rec)
			}
		}
	}

	// Find orphan TXTs: owned TXTs with no companion A/CNAME that are also not desired.
	for key := range owned {
		if _, exists := aOrCnameByKey[key]; !exists {
			if _, wanted := desiredByKey[key]; !wanted {
				txtKey := registry.TXTKey(txtPrefix, key.RecordType, key.Hostname)
				if txtRec, ok := txtByKey[txtKey]; ok {
					changes.OrphanTXT = append(changes.OrphanTXT, txtRec)
				}
			}
		}
	}

	return changes
}
