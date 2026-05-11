package plan

import (
	"testing"

	"github.com/ishioni/dexd/internal/provider/unifi"
	"github.com/ishioni/dexd/internal/registry"
	"github.com/ishioni/dexd/internal/source"
)

const ownerID = "us"

// helpers for building inputs

func endpoint(name, target string) *source.Endpoint {
	return endpointType(name, target, "A")
}

func endpointType(name, target, recordType string) *source.Endpoint {
	return &source.Endpoint{
		DNSName:    name,
		Target:     target,
		RecordType: recordType,
		OwnerID:    ownerID,
		Resource:   "docker/" + name,
	}
}

func aRecord(id, key, value string) unifi.DNSRecord {
	return unifi.DNSRecord{ID: id, Key: key, RecordType: "A", Value: value}
}

func cnameRecord(id, key, value string) unifi.DNSRecord {
	return unifi.DNSRecord{ID: id, Key: key, RecordType: "CNAME", Value: value}
}

func ownedTXT(id, hostname, owner string) unifi.DNSRecord {
	return ownedTXTType("", id, "A", hostname, owner)
}

func ownedTXTWithPrefix(prefix, id, hostname, owner string) unifi.DNSRecord {
	return ownedTXTType(prefix, id, "A", hostname, owner)
}

func ownedTXTType(prefix, id, recordType, hostname, owner string) unifi.DNSRecord {
	return unifi.DNSRecord{
		ID:         id,
		Key:        registry.TXTKey(prefix, recordType, hostname),
		RecordType: "TXT",
		Value:      registry.EncodeTXT(owner, "docker/"+hostname),
	}
}

func TestCompute(t *testing.T) {
	tests := []struct {
		name        string
		desired     []*source.Endpoint
		current     []unifi.DNSRecord
		wantCreate  []string
		wantUpdate  []string
		wantReplace []string
		wantDelete  []string
		wantOrphan  []string
	}{
		{
			name: "empty desired and current",
		},
		{
			name:       "create new record",
			desired:    []*source.Endpoint{endpoint("foo.example.com", "10.0.0.1")},
			wantCreate: []string{"A:foo.example.com"},
		},
		{
			name:    "no-op when desired matches owned current",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.1")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", ownerID),
			},
		},
		{
			name:    "update when desired target differs and we own the record",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.2")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", ownerID),
			},
			wantUpdate: []string{"A:foo.example.com"},
		},
		{
			name:    "no update when target differs but no ownership TXT exists",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.2")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				// no TXT
			},
		},
		{
			name:    "no update when TXT belongs to a different owner",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.2")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", "someone-else"),
			},
		},
		{
			name: "delete owned record no longer desired",
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", ownerID),
			},
			wantDelete: []string{"A:foo.example.com"},
		},
		{
			name: "do NOT delete unowned record (no companion TXT)",
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				// no TXT — created by someone external
			},
		},
		{
			name: "do NOT delete record owned by someone else",
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", "someone-else"),
			},
		},
		{
			name:       "create CNAME from empty current",
			desired:    []*source.Endpoint{endpointType("foo.example.com", "traefik.example.com", "CNAME")},
			wantCreate: []string{"CNAME:foo.example.com"},
		},
		{
			name:    "update owned CNAME when target changes",
			desired: []*source.Endpoint{endpointType("foo.example.com", "new.example.com", "CNAME")},
			current: []unifi.DNSRecord{
				cnameRecord("c1", "foo.example.com", "old.example.com"),
				ownedTXTType("", "t1", "CNAME", "foo.example.com", ownerID),
			},
			wantUpdate: []string{"CNAME:foo.example.com"},
		},
		{
			name: "delete owned CNAME no longer desired",
			current: []unifi.DNSRecord{
				cnameRecord("c1", "foo.example.com", "old.example.com"),
				ownedTXTType("", "t1", "CNAME", "foo.example.com", ownerID),
			},
			wantDelete: []string{"CNAME:foo.example.com"},
		},
		{
			name: "A and CNAME for different hostnames coexist",
			desired: []*source.Endpoint{
				endpoint("a.example.com", "10.0.0.1"),
				endpointType("c.example.com", "target.example.com", "CNAME"),
			},
			current: []unifi.DNSRecord{
				aRecord("a1", "a.example.com", "10.0.0.1"),
				ownedTXT("t1", "a.example.com", ownerID),
				cnameRecord("c1", "c.example.com", "target.example.com"),
				ownedTXTType("", "t2", "CNAME", "c.example.com", ownerID),
			},
		},
		{
			name:    "record type flip replaces old A with new CNAME",
			desired: []*source.Endpoint{endpointType("foo.example.com", "bar.example.com", "CNAME")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				ownedTXT("t1", "foo.example.com", ownerID),
			},
			wantReplace: []string{"A:foo.example.com->CNAME:foo.example.com"},
		},
		{
			name:    "record type flip does not claim unowned old record",
			desired: []*source.Endpoint{endpointType("foo.example.com", "bar.example.com", "CNAME")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
			},
		},
		{
			name: "collision: two desired endpoints for the same hostname, only one create",
			desired: []*source.Endpoint{
				endpointType("foo.example.com", "10.0.0.1", "A"),
				endpointType("foo.example.com", "10.0.0.2", "A"),
			},
			wantCreate: []string{"A:foo.example.com"},
		},
		{
			name:    "unowned existing record: warn but no create, update, or delete",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.1")},
			current: []unifi.DNSRecord{
				aRecord("a1", "foo.example.com", "10.0.0.1"),
				// no TXT — not managed by us
			},
		},
		{
			name:    "orphan TXT with desired hostname: create A, no orphan",
			desired: []*source.Endpoint{endpoint("foo.example.com", "10.0.0.1")},
			current: []unifi.DNSRecord{
				ownedTXT("t1", "foo.example.com", ownerID), // TXT present, A missing
			},
			wantCreate: []string{"A:foo.example.com"},
		},
		{
			name: "orphan TXT with no desired hostname: added to OrphanTXT",
			current: []unifi.DNSRecord{
				ownedTXT("t1", "foo.example.com", ownerID), // TXT present, A missing, not desired
			},
			wantOrphan: []string{"TXT:a-foo.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compute(tt.desired, tt.current, ownerID, "")

			gotCreate := endpointKeys(got.Create)
			gotUpdate := endpointKeys(got.Update)
			gotReplace := replaceKeys(got.Replace)
			gotDelete := recordKeys(got.Delete)
			gotOrphan := recordKeys(got.OrphanTXT)

			if !sameSet(gotCreate, tt.wantCreate) {
				t.Errorf("Create = %v, want %v", gotCreate, tt.wantCreate)
			}
			if !sameSet(gotUpdate, tt.wantUpdate) {
				t.Errorf("Update = %v, want %v", gotUpdate, tt.wantUpdate)
			}
			if !sameSet(gotReplace, tt.wantReplace) {
				t.Errorf("Replace = %v, want %v", gotReplace, tt.wantReplace)
			}
			if !sameSet(gotDelete, tt.wantDelete) {
				t.Errorf("Delete = %v, want %v", gotDelete, tt.wantDelete)
			}
			if !sameSet(gotOrphan, tt.wantOrphan) {
				t.Errorf("OrphanTXT = %v, want %v", gotOrphan, tt.wantOrphan)
			}
		})
	}
}

func TestCompute_WithTXTPrefix(t *testing.T) {
	const prefix = "userprefix."
	desired := []*source.Endpoint{endpoint("foo.example.com", "10.0.0.2")}
	current := []unifi.DNSRecord{
		aRecord("a1", "foo.example.com", "10.0.0.1"),
		ownedTXTWithPrefix(prefix, "t1", "foo.example.com", ownerID),
	}

	got := Compute(desired, current, ownerID, prefix)

	if len(got.Create) != 0 || len(got.Delete) != 0 {
		t.Errorf("expected no create/delete, got create=%v delete=%v", got.Create, got.Delete)
	}
	if len(got.Update) != 1 || got.Update[0].DNSName != "foo.example.com" {
		t.Errorf("expected update for foo.example.com, got %v", got.Update)
	}
}

func endpointKeys(eps []*source.Endpoint) []string {
	out := make([]string, len(eps))
	for i, e := range eps {
		out[i] = e.RecordType + ":" + e.DNSName
	}
	return out
}

func recordKeys(rs []unifi.DNSRecord) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.RecordType + ":" + r.Key
	}
	return out
}

func replaceKeys(replacements []Replace) []string {
	out := make([]string, len(replacements))
	for i, r := range replacements {
		out[i] = r.Old.RecordType + ":" + r.Old.Key + "->" + r.Desired.RecordType + ":" + r.Desired.DNSName
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}
