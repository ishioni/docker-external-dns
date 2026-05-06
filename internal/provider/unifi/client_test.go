package unifi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	testAPIKey = "test-key"
	testSite   = "default"
)

// recordedRequest captures what the client sent so tests can assert on it.
type recordedRequest struct {
	Method string
	Path   string
	APIKey string
	Body   map[string]any
}

func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *[]recordedRequest) {
	t.Helper()
	var recorded []recordedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			APIKey: r.Header.Get("X-Api-Key"),
		}
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			if len(b) > 0 {
				_ = json.Unmarshal(b, &req.Body)
			}
		}
		recorded = append(recorded, req)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, testAPIKey, testSite, false, false)
	return client, &recorded
}

func TestListRecords(t *testing.T) {
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"_id":"abc","key":"foo.example.com","record_type":"A","value":"10.0.0.1","enabled":true}]`))
	})

	got, err := client.ListRecords(context.Background())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != "abc" || got[0].Key != "foo.example.com" {
		t.Errorf("unexpected list result: %+v", got)
	}

	if len(*recorded) != 1 {
		t.Fatalf("expected 1 request, got %d", len(*recorded))
	}
	req := (*recorded)[0]
	if req.Method != http.MethodGet {
		t.Errorf("Method = %s, want GET", req.Method)
	}
	wantPath := "/proxy/network/v2/api/site/" + testSite + "/static-dns"
	if req.Path != wantPath {
		t.Errorf("Path = %s, want %s", req.Path, wantPath)
	}
	if req.APIKey != testAPIKey {
		t.Errorf("X-Api-Key = %q, want %q", req.APIKey, testAPIKey)
	}
}

func TestCreateA_IncludesTTL(t *testing.T) {
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"new-id","key":"foo.example.com","record_type":"A","value":"10.0.0.1","ttl":300,"enabled":true}`))
	})

	got, err := client.CreateRecord(context.Background(), DNSRecord{
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if got.ID != "new-id" {
		t.Errorf("ID = %q, want new-id", got.ID)
	}

	req := (*recorded)[0]
	if req.Method != http.MethodPost {
		t.Errorf("Method = %s, want POST", req.Method)
	}
	if ttl, ok := req.Body["ttl"]; !ok || ttl == nil {
		t.Errorf("A-record body missing ttl: %v", req.Body)
	}
}

func TestCreateTXT_OmitsTTL(t *testing.T) {
	// Regression guard: UniFi rejects TXT records that carry a ttl field.
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"txt-id","key":"a-foo.example.com","record_type":"TXT","value":"\"heritage=external-dns,external-dns/owner=us\"","enabled":true}`))
	})

	_, err := client.CreateRecord(context.Background(), DNSRecord{
		Key:        "a-foo.example.com",
		RecordType: "TXT",
		Value:      `"heritage=external-dns,external-dns/owner=us"`,
	})
	if err != nil {
		t.Fatalf("CreateRecord (TXT): %v", err)
	}

	req := (*recorded)[0]
	if _, present := req.Body["ttl"]; present {
		t.Errorf("TXT record body MUST NOT include ttl, got %v", req.Body)
	}
}

func TestCreateCNAME_IncludesTTL(t *testing.T) {
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"cname-id","key":"foo.example.com","record_type":"CNAME","value":"traefik.example.com","ttl":300,"enabled":true}`))
	})

	_, err := client.CreateRecord(context.Background(), DNSRecord{
		Key:        "foo.example.com",
		RecordType: "CNAME",
		Value:      "traefik.example.com",
	})
	if err != nil {
		t.Fatalf("CreateRecord (CNAME): %v", err)
	}

	req := (*recorded)[0]
	if ttl, ok := req.Body["ttl"]; !ok || ttl == nil {
		t.Errorf("CNAME-record body missing ttl: %v", req.Body)
	}
}

func TestUpdateRecord(t *testing.T) {
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"_id":"abc","key":"foo.example.com","record_type":"A","value":"10.0.0.2","enabled":true}`))
	})

	_, err := client.UpdateRecord(context.Background(), DNSRecord{
		ID:         "abc",
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.2",
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	req := (*recorded)[0]
	if req.Method != http.MethodPut {
		t.Errorf("Method = %s, want PUT", req.Method)
	}
	if !strings.HasSuffix(req.Path, "/static-dns/abc") {
		t.Errorf("Path = %s, expected to end with /static-dns/abc", req.Path)
	}
}

func TestDeleteRecord(t *testing.T) {
	client, recorded := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := client.DeleteRecord(context.Background(), "abc", "foo.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	req := (*recorded)[0]
	if req.Method != http.MethodDelete {
		t.Errorf("Method = %s, want DELETE", req.Method)
	}
	if !strings.HasSuffix(req.Path, "/static-dns/abc") {
		t.Errorf("Path = %s, expected /static-dns/abc suffix", req.Path)
	}
}

func TestErrorPropagation(t *testing.T) {
	client, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorCode":400,"message":"bad request"}`))
	})

	_, err := client.CreateRecord(context.Background(), DNSRecord{
		Key: "foo", RecordType: "A", Value: "10.0.0.1",
	})
	if err == nil {
		t.Fatal("expected error from 400 response, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400: %v", err)
	}
	if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("error should include server body: %v", err)
	}
}

func TestDryRun_NoCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("dry-run client should not make HTTP calls, got %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(srv.Close)

	dryClient := NewClient(srv.URL, testAPIKey, testSite, false, true /* dryRun */)

	if _, err := dryClient.CreateRecord(context.Background(), DNSRecord{
		Key: "foo", RecordType: "A", Value: "10.0.0.1",
	}); err != nil {
		t.Errorf("dry-run CreateRecord error: %v", err)
	}
	if _, err := dryClient.UpdateRecord(context.Background(), DNSRecord{
		ID: "x", Key: "foo", RecordType: "A", Value: "10.0.0.1",
	}); err != nil {
		t.Errorf("dry-run UpdateRecord error: %v", err)
	}
	if err := dryClient.DeleteRecord(context.Background(), "x", "foo", "A"); err != nil {
		t.Errorf("dry-run DeleteRecord error: %v", err)
	}
}
