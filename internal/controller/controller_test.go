package controller

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/ishioni/docker-external-dns/internal/provider/unifi"
	"github.com/ishioni/docker-external-dns/internal/registry"
	"github.com/ishioni/docker-external-dns/internal/source"
)

// ---- fakes ----

type fakeSource struct {
	endpoints []*source.Endpoint
	err       error
	eventCh   chan Event
	errCh     chan error
}

func (f *fakeSource) Endpoints(_ context.Context) ([]*source.Endpoint, error) {
	return f.endpoints, f.err
}

func (f *fakeSource) Events(_ context.Context) (<-chan Event, <-chan error) {
	if f.eventCh == nil {
		ev := make(chan Event)
		errCh := make(chan error)
		close(ev)
		close(errCh)
		return ev, errCh
	}
	return f.eventCh, f.errCh
}

type providerCall struct {
	Op     string
	Record unifi.DNSRecord
}

type fakeProvider struct {
	mu      sync.Mutex
	initial []unifi.DNSRecord
	calls   []providerCall
	nextID  int
	failOn  map[string]error // keyed by "op:key"
}

// snapshot returns a copy of calls safe to read from a different goroutine.
func (f *fakeProvider) snapshot() []providerCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]providerCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeProvider) failKey(op, key string) error {
	if f.failOn == nil {
		return nil
	}
	return f.failOn[op+":"+key]
}

func (f *fakeProvider) ListRecords(_ context.Context) ([]unifi.DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, providerCall{Op: "list"})
	return append([]unifi.DNSRecord(nil), f.initial...), nil
}

func (f *fakeProvider) CreateRecord(_ context.Context, r unifi.DNSRecord) (unifi.DNSRecord, error) {
	err := f.failKey("create", r.Key)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.calls = append(f.calls, providerCall{Op: "create", Record: r})
		return unifi.DNSRecord{}, err
	}
	f.nextID++
	r.ID = fmt.Sprintf("id-%d", f.nextID)
	f.calls = append(f.calls, providerCall{Op: "create", Record: r})
	return r, nil
}

func (f *fakeProvider) UpdateRecord(_ context.Context, r unifi.DNSRecord) (unifi.DNSRecord, error) {
	err := f.failKey("update", r.Key)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, providerCall{Op: "update", Record: r})
	if err != nil {
		return unifi.DNSRecord{}, err
	}
	return r, nil
}

func (f *fakeProvider) DeleteRecord(_ context.Context, id, key, recordType string) error {
	err := f.failKey("delete", key)
	r := unifi.DNSRecord{ID: id, Key: key, RecordType: recordType}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, providerCall{Op: "delete", Record: r})
	return err
}

// ---- helpers ----

const testOwner = "us"

func ep(name, target string) *source.Endpoint {
	return &source.Endpoint{
		DNSName:    name,
		Target:     target,
		RecordType: "A",
		OwnerID:    testOwner,
		Resource:   "docker/" + name,
	}
}

func aRec(id, key, value string) unifi.DNSRecord {
	return unifi.DNSRecord{ID: id, Key: key, RecordType: "A", Value: value, Enabled: true}
}

func ownedTXT(id, hostname, owner string) unifi.DNSRecord {
	return ownedTXTWithPrefix("", id, hostname, owner)
}

func ownedTXTWithPrefix(prefix, id, hostname, owner string) unifi.DNSRecord {
	return unifi.DNSRecord{
		ID:         id,
		Key:        registry.TXTKey(prefix, "A", hostname),
		RecordType: "TXT",
		Value:      registry.EncodeTXT(owner, "docker/"+hostname),
	}
}

func opKeys(calls []providerCall, op string) []string {
	var out []string
	for _, c := range calls {
		if c.Op == op {
			out = append(out, c.Record.Key)
		}
	}
	return out
}

func countOp(calls []providerCall, op string) int {
	n := 0
	for _, c := range calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

func newCtrl(src *fakeSource, prov *fakeProvider) *Controller {
	return New(src, prov, testOwner, "", time.Hour)
}

// ---- reconcile tests ----

func TestReconcile_CreatesNewPair(t *testing.T) {
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")}}
	prov := &fakeProvider{}
	newCtrl(src, prov).reconcile(context.Background())

	creates := opKeys(prov.calls, "create")
	if !containsAll(creates, []string{"foo.example.com", "a-foo.example.com"}) {
		t.Errorf("expected creates for A and TXT, got %v", creates)
	}
	if countOp(prov.calls, "update") != 0 || countOp(prov.calls, "delete") != 0 {
		t.Errorf("unexpected update/delete calls: %v", prov.calls)
	}
}

func TestReconcile_NoOpWhenMatching(t *testing.T) {
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")}}
	prov := &fakeProvider{
		initial: []unifi.DNSRecord{
			aRec("a1", "foo.example.com", "10.0.0.1"),
			ownedTXT("t1", "foo.example.com", testOwner),
		},
	}
	newCtrl(src, prov).reconcile(context.Background())

	if countOp(prov.calls, "create") != 0 || countOp(prov.calls, "update") != 0 || countOp(prov.calls, "delete") != 0 {
		t.Errorf("expected no-op, got calls: %v", prov.calls)
	}
}

func TestReconcile_UpdatesOwnedPair(t *testing.T) {
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.2")}}
	prov := &fakeProvider{
		initial: []unifi.DNSRecord{
			aRec("a1", "foo.example.com", "10.0.0.1"),
			ownedTXT("t1", "foo.example.com", testOwner),
		},
	}
	newCtrl(src, prov).reconcile(context.Background())

	updates := opKeys(prov.calls, "update")
	if !containsAll(updates, []string{"foo.example.com", "a-foo.example.com"}) {
		t.Errorf("expected updates for A and TXT, got %v", updates)
	}
	// Verify the A record gets the new IP.
	for _, c := range prov.calls {
		if c.Op == "update" && c.Record.Key == "foo.example.com" && c.Record.Value != "10.0.0.2" {
			t.Errorf("A record updated with wrong value: %q", c.Record.Value)
		}
	}
	if countOp(prov.calls, "create") != 0 || countOp(prov.calls, "delete") != 0 {
		t.Errorf("unexpected create/delete calls: %v", prov.calls)
	}
}

func TestReconcile_DeletesOwnedPair(t *testing.T) {
	src := &fakeSource{endpoints: nil}
	prov := &fakeProvider{
		initial: []unifi.DNSRecord{
			aRec("a1", "foo.example.com", "10.0.0.1"),
			ownedTXT("t1", "foo.example.com", testOwner),
		},
	}
	newCtrl(src, prov).reconcile(context.Background())

	deletes := opKeys(prov.calls, "delete")
	if !containsAll(deletes, []string{"foo.example.com", "a-foo.example.com"}) {
		t.Errorf("expected deletes for A and TXT, got %v", deletes)
	}
	if countOp(prov.calls, "create") != 0 || countOp(prov.calls, "update") != 0 {
		t.Errorf("unexpected create/update calls: %v", prov.calls)
	}
}

func TestReconcile_SkipsUnownedRecord(t *testing.T) {
	src := &fakeSource{endpoints: nil}
	prov := &fakeProvider{
		initial: []unifi.DNSRecord{
			aRec("a1", "foo.example.com", "10.0.0.1"),
			// no TXT — not managed by us
		},
	}
	newCtrl(src, prov).reconcile(context.Background())

	if countOp(prov.calls, "delete") != 0 {
		t.Errorf("must not delete unowned record, got: %v", prov.calls)
	}
}

func TestReconcile_SkipsForeignOwnedRecord(t *testing.T) {
	src := &fakeSource{endpoints: nil}
	prov := &fakeProvider{
		initial: []unifi.DNSRecord{
			aRec("a1", "foo.example.com", "10.0.0.1"),
			ownedTXT("t1", "foo.example.com", "someone-else"),
		},
	}
	newCtrl(src, prov).reconcile(context.Background())

	if countOp(prov.calls, "delete") != 0 {
		t.Errorf("must not delete record owned by another agent, got: %v", prov.calls)
	}
}

func TestReconcile_ContinuesAfterCreateError(t *testing.T) {
	src := &fakeSource{endpoints: []*source.Endpoint{
		ep("foo.example.com", "10.0.0.1"),
		ep("bar.example.com", "10.0.0.1"),
	}}
	prov := &fakeProvider{
		failOn: map[string]error{"create:foo.example.com": fmt.Errorf("injected error")},
	}
	newCtrl(src, prov).reconcile(context.Background())

	// Both hostnames must have been attempted despite the first error.
	attempted := make(map[string]bool)
	for _, c := range prov.calls {
		if c.Op == "create" {
			attempted[c.Record.Key] = true
		}
	}
	if !attempted["foo.example.com"] {
		t.Error("foo.example.com create was not attempted")
	}
	if !attempted["bar.example.com"] {
		t.Error("bar.example.com create was not attempted after foo failure")
	}
}

// TestRun_DebouncesEventsAndReconciles verifies that a Docker event
// triggers a second reconcile after the 2s debounce window.
func TestRun_DebouncesEventsAndReconciles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping debounce test in short mode (needs ~3s)")
	}

	evCh := make(chan Event, 1)
	errCh := make(chan error)
	src := &fakeSource{
		endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")},
		eventCh:   evCh,
		errCh:     errCh,
	}
	prov := &fakeProvider{}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctrl := New(src, prov, testOwner, "", time.Hour)
	go ctrl.Run(ctx)

	// Emit one event to trigger the debounce path.
	evCh <- Event{Action: "start", Name: "whoami"}

	// Poll until we see at least 2 list calls (initial + post-debounce).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countOp(prov.snapshot(), "list") >= 2 {
			cancel()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("expected ≥2 list calls after debounce, got %d", countOp(prov.snapshot(), "list"))
}

func TestReconcile_RespectsTXTPrefix(t *testing.T) {
	const prefix = "userprefix."
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")}}
	prov := &fakeProvider{}
	ctrl := New(src, prov, testOwner, prefix, time.Hour)
	ctrl.reconcile(context.Background())

	creates := opKeys(prov.calls, "create")
	wantTXTKey := "userprefix.a-foo.example.com"
	if !containsAll(creates, []string{"foo.example.com", wantTXTKey}) {
		t.Errorf("expected TXT key %q with prefix, got creates: %v", wantTXTKey, creates)
	}
}

// ---- set helper ----

func containsAll(haystack, needles []string) bool {
	m := make(map[string]bool, len(haystack))
	for _, s := range haystack {
		m[s] = true
	}
	for _, n := range needles {
		if !m[n] {
			return false
		}
	}
	return true
}
