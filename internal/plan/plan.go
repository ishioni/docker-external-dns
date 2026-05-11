// Package plan computes the minimal set of DNS changes needed to bring
// the current UniFi records into sync with the desired state derived from
// running containers.
package plan

import (
	"log/slog"

	"github.com/ishioni/dexd/internal/provider/unifi"
	"github.com/ishioni/dexd/internal/registry"
	"github.com/ishioni/dexd/internal/source"
)

// Changes holds the buckets of DNS work to perform.
type Changes struct {
	// Create holds endpoints that don't yet have a matching record in UniFi.
	Create []*source.Endpoint
	// Update holds endpoints whose record exists but has a different target value.
	Update []*source.Endpoint
	// Replace holds endpoints whose hostname is already owned by us under a
	// different record type and must be replaced before the desired record can be created.
	Replace []Replace
	// Delete holds A/CNAME records owned by us that are no longer in the desired set.
	Delete []unifi.DNSRecord
	// OrphanTXT holds TXT ownership records we own that have no companion A/CNAME
	// and are no longer desired. They should be deleted to keep UniFi clean.
	OrphanTXT []unifi.DNSRecord
}

type Replace struct {
	Old     unifi.DNSRecord
	Desired *source.Endpoint
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
	replacedOld := make(map[recordKey]bool)
	for key, ep := range desiredByKey {
		existing, exists := aOrCnameByKey[key]
		if !exists {
			if old, ok := ownedOtherType(key, desiredByKey, aOrCnameByKey, owned); ok {
				changes.Replace = append(changes.Replace, Replace{Old: old, Desired: ep})
				replacedOld[recordKey{Hostname: old.Key, RecordType: old.RecordType}] = true
				continue
			}
			if other, ok := otherTypeRecord(key, aOrCnameByKey); ok {
				slog.Warn("unowned record at desired hostname with different type; skipping until manually resolved",
					"hostname", key.Hostname,
					"current_record_type", other.RecordType,
					"desired_record_type", key.RecordType,
					"current_value", other.Value,
					"desired_value", ep.Target,
				)
				continue
			}
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
		if replacedOld[key] {
			continue
		}
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

func ownedOtherType(
	key recordKey,
	desiredByKey map[recordKey]*source.Endpoint,
	aOrCnameByKey map[recordKey]unifi.DNSRecord,
	owned map[recordKey]bool,
) (unifi.DNSRecord, bool) {
	for _, recordType := range []string{"A", "CNAME"} {
		if recordType == key.RecordType {
			continue
		}
		oldKey := recordKey{Hostname: key.Hostname, RecordType: recordType}
		if _, wanted := desiredByKey[oldKey]; wanted {
			continue
		}
		rec, exists := aOrCnameByKey[oldKey]
		if exists && owned[oldKey] {
			return rec, true
		}
	}
	return unifi.DNSRecord{}, false
}

func otherTypeRecord(key recordKey, aOrCnameByKey map[recordKey]unifi.DNSRecord) (unifi.DNSRecord, bool) {
	for _, recordType := range []string{"A", "CNAME"} {
		if recordType == key.RecordType {
			continue
		}
		rec, exists := aOrCnameByKey[recordKey{Hostname: key.Hostname, RecordType: recordType}]
		if exists {
			return rec, true
		}
	}
	return unifi.DNSRecord{}, false
}
