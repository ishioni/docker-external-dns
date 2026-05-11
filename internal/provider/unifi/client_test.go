package unifi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type recordedRequest struct {
	Method      string
	Path        string
	APIKey      string
	Accept      string
	ContentType string
	Body        map[string]any
}

type strictUniFiServer struct {
	t        *testing.T
	server   *httptest.Server
	records  map[string]DNSRecord
	requests []recordedRequest
	nextID   int

	failMethod int
	failStatus int
	failBody   errorResponse

	rawStatus int
	rawBody   string
}

func newStrictUniFiServer(t *testing.T, initial ...DNSRecord) *strictUniFiServer {
	t.Helper()
	api := &strictUniFiServer{
		t:       t,
		records: make(map[string]DNSRecord),
	}
	for _, r := range initial {
		api.records[r.ID] = r
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	return api
}

func (s *strictUniFiServer) client(dryRun bool) *Client {
	return NewClient(s.server.URL, testAPIKey, testSite, false, dryRun, 300)
}

func (s *strictUniFiServer) failNext(method string, status int, message string) {
	s.failMethod = methodCode(method)
	s.failStatus = status
	s.failBody = errorResponse{Code: "ERROR", ErrorCode: status, Message: message}
}

func (s *strictUniFiServer) respondRaw(status int, body string) {
	s.rawStatus = status
	s.rawBody = body
}

func (s *strictUniFiServer) handle(w http.ResponseWriter, r *http.Request) {
	if s.rawStatus != 0 {
		w.WriteHeader(s.rawStatus)
		_, _ = w.Write([]byte(s.rawBody))
		return
	}

	req := s.recordRequest(r)
	s.requests = append(s.requests, req)

	if req.APIKey != testAPIKey {
		s.writeError(w, http.StatusUnauthorized, "missing or invalid API key")
		return
	}
	if req.Accept != "application/json" {
		s.writeError(w, http.StatusBadRequest, "missing JSON accept header")
		return
	}
	if req.ContentType != "application/json; charset=utf-8" {
		s.writeError(w, http.StatusBadRequest, "missing JSON content type")
		return
	}

	if s.failMethod == methodCode(r.Method) {
		s.failMethod = 0
		w.WriteHeader(s.failStatus)
		_ = json.NewEncoder(w).Encode(s.failBody)
		return
	}

	base := "/proxy/network/v2/api/site/" + testSite + "/static-dns"
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		s.list(w)
	case r.Method == http.MethodPost && r.URL.Path == base:
		s.create(w, req.Body)
	case strings.HasPrefix(r.URL.Path, base+"/"):
		id := strings.TrimPrefix(r.URL.Path, base+"/")
		switch r.Method {
		case http.MethodPut:
			s.update(w, id, req.Body)
		case http.MethodDelete:
			s.delete(w, id)
		default:
			s.writeError(w, http.StatusMethodNotAllowed, "unsupported method")
		}
	default:
		s.writeError(w, http.StatusNotFound, "unexpected path")
	}
}

func (s *strictUniFiServer) recordRequest(r *http.Request) recordedRequest {
	req := recordedRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		APIKey:      r.Header.Get("X-Api-Key"),
		Accept:      r.Header.Get("Accept"),
		ContentType: r.Header.Get("Content-Type"),
	}
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		if len(b) > 0 {
			_ = json.Unmarshal(b, &req.Body)
		}
	}
	return req
}

func (s *strictUniFiServer) list(w http.ResponseWriter) {
	records := make([]DNSRecord, 0, len(s.records))
	for _, r := range s.records {
		records = append(records, r)
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(records)
}

func (s *strictUniFiServer) create(w http.ResponseWriter, body map[string]any) {
	if !s.validRecordBody(w, body) {
		return
	}
	s.nextID++
	record := recordFromBody(fmt.Sprintf("id-%d", s.nextID), body)
	s.records[record.ID] = record
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(record)
}

func (s *strictUniFiServer) update(w http.ResponseWriter, id string, body map[string]any) {
	if _, ok := s.records[id]; !ok {
		s.writeError(w, http.StatusNotFound, "record not found")
		return
	}
	if !s.validRecordBody(w, body) {
		return
	}
	record := recordFromBody(id, body)
	s.records[id] = record
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(record)
}

func (s *strictUniFiServer) delete(w http.ResponseWriter, id string) {
	if _, ok := s.records[id]; !ok {
		s.writeError(w, http.StatusNotFound, "record not found")
		return
	}
	delete(s.records, id)
	w.WriteHeader(http.StatusOK)
}

func (s *strictUniFiServer) validRecordBody(w http.ResponseWriter, body map[string]any) bool {
	if body["key"] == "" || body["record_type"] == "" || body["value"] == "" {
		s.writeError(w, http.StatusBadRequest, "missing required record fields")
		return false
	}
	if enabled, ok := body["enabled"].(bool); !ok || !enabled {
		s.writeError(w, http.StatusBadRequest, "record must be enabled")
		return false
	}

	recordType, _ := body["record_type"].(string)
	_, hasTTL := body["ttl"]
	switch recordType {
	case "A", "CNAME":
	case "TXT":
		if hasTTL {
			s.writeError(w, http.StatusBadRequest, "TXT records must not include ttl")
			return false
		}
	default:
		s.writeError(w, http.StatusBadRequest, "unsupported record type")
		return false
	}
	return true
}

func (s *strictUniFiServer) writeError(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Code:      "ERROR",
		ErrorCode: status,
		Message:   message,
	})
}

func recordFromBody(id string, body map[string]any) DNSRecord {
	ttl := 0
	if rawTTL, ok := body["ttl"].(float64); ok {
		ttl = int(rawTTL)
	}
	return DNSRecord{
		ID:         id,
		Key:        body["key"].(string),
		RecordType: body["record_type"].(string),
		Value:      body["value"].(string),
		TTL:        ttl,
		Enabled:    body["enabled"].(bool),
	}
}

func methodCode(method string) int {
	switch method {
	case http.MethodGet:
		return 1
	case http.MethodPost:
		return 2
	case http.MethodPut:
		return 3
	case http.MethodDelete:
		return 4
	default:
		return 0
	}
}

func TestListRecords(t *testing.T) {
	api := newStrictUniFiServer(t,
		DNSRecord{ID: "a1", Key: "foo.example.com", RecordType: "A", Value: "10.0.0.1", TTL: 300, Enabled: true},
		DNSRecord{ID: "c1", Key: "alias.example.com", RecordType: "CNAME", Value: "target.example.com", TTL: 300, Enabled: true},
		DNSRecord{ID: "t1", Key: "a-foo.example.com", RecordType: "TXT", Value: `"heritage=external-dns"`, Enabled: true},
	)

	got, err := api.client(false).ListRecords(context.Background())
	if err != nil {
		t.Fatalf("ListRecords: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ListRecords returned %d records, want 3", len(got))
	}

	req := api.requests[0]
	wantPath := "/proxy/network/v2/api/site/" + testSite + "/static-dns"
	if req.Method != http.MethodGet || req.Path != wantPath {
		t.Fatalf("request = %s %s, want GET %s", req.Method, req.Path, wantPath)
	}
}

func TestListRecords_InvalidJSONReturnsDataError(t *testing.T) {
	api := newStrictUniFiServer(t)
	api.respondRaw(http.StatusOK, `{"invalid": json}`)

	_, err := api.client(false).ListRecords(context.Background())
	var dataErr *DataError
	if !errors.As(err, &dataErr) {
		t.Fatalf("ListRecords error = %T %v, want *DataError", err, err)
	}
}

func TestListRecords_APIErrorIncludesUniFiMessage(t *testing.T) {
	api := newStrictUniFiServer(t)
	api.failNext(http.MethodGet, http.StatusInternalServerError, "Internal server error")

	_, err := api.client(false).ListRecords(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ListRecords error = %T %v, want *APIError", err, err)
	}
	if apiErr.StatusCode != http.StatusInternalServerError || apiErr.Message != "Internal server error" {
		t.Fatalf("APIError = %+v, want status/message from UniFi body", apiErr)
	}
}

func TestCreateA_DefaultTTLAutoOmitsTTLAndIncludesHeaders(t *testing.T) {
	api := newStrictUniFiServer(t)

	got, err := NewClient(api.server.URL, testAPIKey, testSite, false, false, 0).CreateRecord(context.Background(), DNSRecord{
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateRecord: %v", err)
	}
	if got.ID == "" {
		t.Fatal("created record ID is empty")
	}

	req := api.requests[0]
	if req.Method != http.MethodPost {
		t.Fatalf("Method = %s, want POST", req.Method)
	}
	if req.APIKey != testAPIKey || req.Accept != "application/json" || req.ContentType != "application/json; charset=utf-8" {
		t.Fatalf("unexpected headers: %+v", req)
	}
	if _, present := req.Body["ttl"]; present {
		t.Fatalf("A record ttl = %v, want omitted for auto", req.Body["ttl"])
	}
	if req.Body["_id"] != nil {
		t.Fatalf("create body must not include _id, got %v", req.Body)
	}
}

func TestCreateA_NumericDefaultTTLIncludesTTL(t *testing.T) {
	api := newStrictUniFiServer(t)

	_, err := api.client(false).CreateRecord(context.Background(), DNSRecord{
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("CreateRecord (A): %v", err)
	}

	req := api.requests[0]
	if ttl, ok := req.Body["ttl"].(float64); !ok || int(ttl) != 300 {
		t.Fatalf("A record ttl = %v, want 300", req.Body["ttl"])
	}
}

func TestCreateCNAME_NumericDefaultTTLIncludesTTL(t *testing.T) {
	api := newStrictUniFiServer(t)

	_, err := api.client(false).CreateRecord(context.Background(), DNSRecord{
		Key:        "alias.example.com",
		RecordType: "CNAME",
		Value:      "target.example.com",
	})
	if err != nil {
		t.Fatalf("CreateRecord (CNAME): %v", err)
	}

	req := api.requests[0]
	if ttl, ok := req.Body["ttl"].(float64); !ok || int(ttl) != 300 {
		t.Fatalf("CNAME record ttl = %v, want 300", req.Body["ttl"])
	}
}

func TestCreateTXT_OmitsTTL(t *testing.T) {
	api := newStrictUniFiServer(t)

	_, err := api.client(false).CreateRecord(context.Background(), DNSRecord{
		Key:        "a-foo.example.com",
		RecordType: "TXT",
		Value:      `"heritage=external-dns,external-dns/owner=us"`,
	})
	if err != nil {
		t.Fatalf("CreateRecord (TXT): %v", err)
	}

	req := api.requests[0]
	if _, present := req.Body["ttl"]; present {
		t.Fatalf("TXT record body MUST NOT include ttl, got %v", req.Body)
	}
	if req.Body["value"] != `"heritage=external-dns,external-dns/owner=us"` {
		t.Fatalf("TXT value = %q, want quoted ownership value", req.Body["value"])
	}
}

func TestValidateRecordRejectsWildcardCNAME(t *testing.T) {
	client := NewClient("https://unifi.example.com", testAPIKey, testSite, false, false, 0)

	err := client.ValidateRecord(DNSRecord{
		Key:        "*.example.com",
		RecordType: "CNAME",
		Value:      "target.example.com",
	})
	var unsupported *UnsupportedRecordError
	if !errors.As(err, &unsupported) {
		t.Fatalf("ValidateRecord error = %T %v, want *UnsupportedRecordError", err, err)
	}
}

func TestValidateRecordAllowsWildcardA(t *testing.T) {
	client := NewClient("https://unifi.example.com", testAPIKey, testSite, false, false, 0)

	err := client.ValidateRecord(DNSRecord{
		Key:        "*.example.com",
		RecordType: "A",
		Value:      "10.0.0.1",
	})
	if err != nil {
		t.Fatalf("ValidateRecord error = %v, want nil", err)
	}
}

func TestUpdateRecord(t *testing.T) {
	api := newStrictUniFiServer(t, DNSRecord{
		ID: "abc", Key: "foo.example.com", RecordType: "A", Value: "10.0.0.1", TTL: 300, Enabled: true,
	})

	_, err := api.client(false).UpdateRecord(context.Background(), DNSRecord{
		ID:         "abc",
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.2",
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	req := api.requests[0]
	if req.Method != http.MethodPut || !strings.HasSuffix(req.Path, "/static-dns/abc") {
		t.Fatalf("request = %s %s, want PUT .../static-dns/abc", req.Method, req.Path)
	}
	if api.records["abc"].Value != "10.0.0.2" {
		t.Fatalf("updated value = %q, want 10.0.0.2", api.records["abc"].Value)
	}
}

func TestUpdateA_DefaultTTLAutoOmitsTTL(t *testing.T) {
	api := newStrictUniFiServer(t, DNSRecord{
		ID: "abc", Key: "foo.example.com", RecordType: "A", Value: "10.0.0.1", TTL: 300, Enabled: true,
	})

	_, err := NewClient(api.server.URL, testAPIKey, testSite, false, false, 0).UpdateRecord(context.Background(), DNSRecord{
		ID:         "abc",
		Key:        "foo.example.com",
		RecordType: "A",
		Value:      "10.0.0.2",
	})
	if err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	req := api.requests[0]
	if _, present := req.Body["ttl"]; present {
		t.Fatalf("A record update ttl = %v, want omitted for auto", req.Body["ttl"])
	}
}

func TestDeleteRecord(t *testing.T) {
	api := newStrictUniFiServer(t, DNSRecord{
		ID: "abc", Key: "foo.example.com", RecordType: "A", Value: "10.0.0.1", TTL: 300, Enabled: true,
	})

	if err := api.client(false).DeleteRecord(context.Background(), "abc", "foo.example.com", "A"); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}

	req := api.requests[0]
	if req.Method != http.MethodDelete || !strings.HasSuffix(req.Path, "/static-dns/abc") {
		t.Fatalf("request = %s %s, want DELETE .../static-dns/abc", req.Method, req.Path)
	}
	if _, exists := api.records["abc"]; exists {
		t.Fatal("record abc still exists after delete")
	}
}

func TestNetworkError(t *testing.T) {
	client := NewClient("https://unifi.example.com", testAPIKey, testSite, false, false, 0)
	client.http.Transport = failingTransport{}

	_, err := client.ListRecords(context.Background())
	var networkErr *NetworkError
	if !errors.As(err, &networkErr) {
		t.Fatalf("ListRecords error = %T %v, want *NetworkError", err, err)
	}
}

func TestDryRun_NoMutationCalls(t *testing.T) {
	api := newStrictUniFiServer(t)
	client := api.client(true)

	if _, err := client.CreateRecord(context.Background(), DNSRecord{Key: "foo", RecordType: "A", Value: "10.0.0.1"}); err != nil {
		t.Errorf("dry-run CreateRecord error: %v", err)
	}
	if _, err := client.UpdateRecord(context.Background(), DNSRecord{ID: "x", Key: "foo", RecordType: "A", Value: "10.0.0.1"}); err != nil {
		t.Errorf("dry-run UpdateRecord error: %v", err)
	}
	if err := client.DeleteRecord(context.Background(), "x", "foo", "A"); err != nil {
		t.Errorf("dry-run DeleteRecord error: %v", err)
	}
	if len(api.requests) != 0 {
		t.Fatalf("dry-run mutations made HTTP requests: %+v", api.requests)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}
