package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ishioni/docker-external-dns/internal/config"
	"github.com/ishioni/docker-external-dns/internal/provider/unifi"
	"github.com/ishioni/docker-external-dns/internal/registry"
	"github.com/ishioni/docker-external-dns/internal/source"
)

type unifiHTTPCall struct {
	Method string
	Path   string
	Body   map[string]any
}

type controllerUniFiAPI struct {
	t          *testing.T
	server     *httptest.Server
	records    map[string]unifi.DNSRecord
	nextID     int
	calls      []unifiHTTPCall
	failCreate string
}

func newControllerUniFiAPI(t *testing.T, records ...unifi.DNSRecord) *controllerUniFiAPI {
	t.Helper()
	api := &controllerUniFiAPI{
		t:       t,
		records: make(map[string]unifi.DNSRecord),
	}
	for _, r := range records {
		api.records[r.ID] = r
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	return api
}

func (api *controllerUniFiAPI) provider() *unifi.Client {
	return unifi.NewClient(api.server.URL, "test-key", "default", false, false)
}

func (api *controllerUniFiAPI) handle(w http.ResponseWriter, r *http.Request) {
	call := unifiHTTPCall{Method: r.Method, Path: r.URL.Path}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&call.Body)
	}
	api.calls = append(api.calls, call)

	base := "/proxy/network/v2/api/site/default/static-dns"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		records := make([]unifi.DNSRecord, 0, len(api.records))
		for _, rec := range api.records {
			records = append(records, rec)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(records)
	case r.Method == http.MethodPost && r.URL.Path == base:
		api.create(w, call.Body)
	case strings.HasPrefix(r.URL.Path, base+"/"):
		id := strings.TrimPrefix(r.URL.Path, base+"/")
		switch r.Method {
		case http.MethodPut:
			api.update(w, id, call.Body)
		case http.MethodDelete:
			api.delete(w, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (api *controllerUniFiAPI) create(w http.ResponseWriter, body map[string]any) {
	key, _ := body["key"].(string)
	if api.failCreate == key {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "injected create failure"})
		return
	}
	api.nextID++
	record := unifiRecordFromBody(fmt.Sprintf("new-%d", api.nextID), body)
	api.records[record.ID] = record
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(record)
}

func (api *controllerUniFiAPI) update(w http.ResponseWriter, id string, body map[string]any) {
	record := unifiRecordFromBody(id, body)
	api.records[id] = record
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(record)
}

func (api *controllerUniFiAPI) delete(w http.ResponseWriter, id string) {
	if record, ok := api.records[id]; ok {
		api.calls[len(api.calls)-1].Body = map[string]any{"key": record.Key}
	}
	delete(api.records, id)
	w.WriteHeader(http.StatusOK)
}

func unifiRecordFromBody(id string, body map[string]any) unifi.DNSRecord {
	ttl := 0
	if rawTTL, ok := body["ttl"].(float64); ok {
		ttl = int(rawTTL)
	}
	return unifi.DNSRecord{
		ID:         id,
		Key:        body["key"].(string),
		RecordType: body["record_type"].(string),
		Value:      body["value"].(string),
		TTL:        ttl,
		Enabled:    body["enabled"].(bool),
	}
}

func TestReconcileWithUniFiClient_CreatesTXTBeforeRecord(t *testing.T) {
	api := newControllerUniFiAPI(t)
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")}}

	New(src, api.provider(), testOwner, "", config.PolicySync, time.Hour).reconcile(context.Background())

	assertHTTPBefore(t, api.calls, http.MethodPost, "a-foo.example.com", http.MethodPost, "foo.example.com")
	assertHTTPBodyHasNoTTL(t, api.calls, "a-foo.example.com")
	assertHTTPBodyTTL(t, api.calls, "foo.example.com", 300)
}

func TestReconcileWithUniFiClient_ReplacesTypeBeforeCreate(t *testing.T) {
	api := newControllerUniFiAPI(t,
		unifi.DNSRecord{ID: "a1", Key: "foo.example.com", RecordType: "A", Value: "10.0.0.1", TTL: 300, Enabled: true},
		unifi.DNSRecord{
			ID:         "t1",
			Key:        registry.TXTKey("", "A", "foo.example.com"),
			RecordType: "TXT",
			Value:      registry.EncodeTXT(testOwner, "docker/foo.example.com"),
			Enabled:    true,
		},
	)
	src := &fakeSource{endpoints: []*source.Endpoint{epType("foo.example.com", "target.example.com", "CNAME")}}

	New(src, api.provider(), testOwner, "", config.PolicySync, time.Hour).reconcile(context.Background())

	assertHTTPBefore(t, api.calls, http.MethodDelete, "foo.example.com", http.MethodPost, "cname-foo.example.com")
	assertHTTPBefore(t, api.calls, http.MethodDelete, "a-foo.example.com", http.MethodPost, "cname-foo.example.com")
	assertHTTPBefore(t, api.calls, http.MethodPost, "cname-foo.example.com", http.MethodPost, "foo.example.com")
	assertHTTPBodyTTL(t, api.calls, "foo.example.com", 300)
}

func TestReconcileWithUniFiClient_TXTFailurePreventsRecordCreate(t *testing.T) {
	api := newControllerUniFiAPI(t)
	api.failCreate = "a-foo.example.com"
	src := &fakeSource{endpoints: []*source.Endpoint{ep("foo.example.com", "10.0.0.1")}}

	New(src, api.provider(), testOwner, "", config.PolicySync, time.Hour).reconcile(context.Background())

	if idx := httpCallIndex(api.calls, http.MethodPost, "foo.example.com"); idx >= 0 {
		t.Fatalf("A record was created after TXT create failure, calls: %+v", api.calls)
	}
}

func assertHTTPBefore(t *testing.T, calls []unifiHTTPCall, firstMethod, firstKey, secondMethod, secondKey string) {
	t.Helper()
	first := httpCallIndex(calls, firstMethod, firstKey)
	second := httpCallIndex(calls, secondMethod, secondKey)
	if first < 0 {
		t.Fatalf("missing %s call for %s in %+v", firstMethod, firstKey, calls)
	}
	if second < 0 {
		t.Fatalf("missing %s call for %s in %+v", secondMethod, secondKey, calls)
	}
	if first >= second {
		t.Fatalf("expected %s %s before %s %s, got %+v", firstMethod, firstKey, secondMethod, secondKey, calls)
	}
}

func assertHTTPBodyTTL(t *testing.T, calls []unifiHTTPCall, key string, want int) {
	t.Helper()
	call := httpCallByKey(t, calls, key)
	ttl, ok := call.Body["ttl"].(float64)
	if !ok || int(ttl) != want {
		t.Fatalf("ttl for %s = %v, want %d in %+v", key, call.Body["ttl"], want, call.Body)
	}
}

func assertHTTPBodyHasNoTTL(t *testing.T, calls []unifiHTTPCall, key string) {
	t.Helper()
	call := httpCallByKey(t, calls, key)
	if _, exists := call.Body["ttl"]; exists {
		t.Fatalf("%s body has ttl, want omitted: %+v", key, call.Body)
	}
}

func httpCallByKey(t *testing.T, calls []unifiHTTPCall, key string) unifiHTTPCall {
	t.Helper()
	for _, call := range calls {
		if call.Method == http.MethodPost && call.Body["key"] == key {
			return call
		}
	}
	t.Fatalf("missing call body for key %s in %+v", key, calls)
	return unifiHTTPCall{}
}

func httpCallIndex(calls []unifiHTTPCall, method, key string) int {
	for i, call := range calls {
		if call.Method == method && call.Body["key"] == key {
			return i
		}
	}
	return -1
}
