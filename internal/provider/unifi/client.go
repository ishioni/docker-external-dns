package unifi

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"time"
)

const defaultTTL = 300

// Client talks to the UniFi OS static-dns REST API.
type Client struct {
	http    *http.Client
	baseURL string
	site    string
	apiKey  string
	dryRun  bool
}

// NewClient creates an authenticated UniFi client.
// It uses X-Api-Key header auth (UniFi Network 9.0+ PAT).
func NewClient(host, apiKey, site string, insecureSkipVerify, dryRun bool) *Client {
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify}, //nolint:gosec
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			Jar:       jar,
			Timeout:   15 * time.Second,
		},
		baseURL: fmt.Sprintf("%s/proxy/network/v2/api/site/%s/static-dns", host, site),
		site:    site,
		apiKey:  apiKey,
		dryRun:  dryRun,
	}
}

// ListRecords returns all static DNS records from the controller.
func (c *Client) ListRecords(ctx context.Context) ([]DNSRecord, error) {
	var records listResponse
	if err := c.do(ctx, http.MethodGet, c.baseURL, nil, &records); err != nil {
		return nil, fmt.Errorf("list records: %w", err)
	}
	slog.Debug("listed unifi records", "count", len(records))
	return records, nil
}

// CreateRecord creates a new static DNS record and returns it (with _id populated).
func (c *Client) CreateRecord(ctx context.Context, r DNSRecord) (DNSRecord, error) {
	if r.RecordType != "TXT" && r.TTL == 0 {
		r.TTL = defaultTTL
	}
	r.Enabled = true

	if c.dryRun {
		slog.Info("[dry-run] would create record", "key", r.Key, "type", r.RecordType, "value", r.Value)
		return r, nil
	}

	var created DNSRecord
	if err := c.do(ctx, http.MethodPost, c.baseURL, r, &created); err != nil {
		return DNSRecord{}, fmt.Errorf("create record %s %s: %w", r.RecordType, r.Key, err)
	}
	slog.Info("created record", "key", created.Key, "type", created.RecordType, "value", created.Value, "id", created.ID)
	return created, nil
}

// UpdateRecord replaces a record by ID.
func (c *Client) UpdateRecord(ctx context.Context, r DNSRecord) (DNSRecord, error) {
	if r.RecordType != "TXT" && r.TTL == 0 {
		r.TTL = defaultTTL
	}
	r.Enabled = true

	url := fmt.Sprintf("%s/%s", c.baseURL, r.ID)
	if c.dryRun {
		slog.Info("[dry-run] would update record", "key", r.Key, "type", r.RecordType, "value", r.Value, "id", r.ID)
		return r, nil
	}

	var updated DNSRecord
	if err := c.do(ctx, http.MethodPut, url, r, &updated); err != nil {
		return DNSRecord{}, fmt.Errorf("update record %s %s: %w", r.RecordType, r.Key, err)
	}
	slog.Info("updated record", "key", updated.Key, "type", updated.RecordType, "value", updated.Value, "id", updated.ID)
	return updated, nil
}

// DeleteRecord removes a record by ID.
func (c *Client) DeleteRecord(ctx context.Context, id, key, recordType string) error {
	url := fmt.Sprintf("%s/%s", c.baseURL, id)
	if c.dryRun {
		slog.Info("[dry-run] would delete record", "key", key, "type", recordType, "id", id)
		return nil
	}

	if err := c.do(ctx, http.MethodDelete, url, nil, nil); err != nil {
		return fmt.Errorf("delete record %s %s (id=%s): %w", recordType, key, id, err)
	}
	slog.Info("deleted record", "key", key, "type", recordType, "id", id)
	return nil
}

func (c *Client) do(ctx context.Context, method, url string, body, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode response: %w (body: %s)", err, string(respBody))
		}
	}
	return nil
}
