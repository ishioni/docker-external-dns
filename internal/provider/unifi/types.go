package unifi

// DNSRecord is the wire format for UniFi's static-dns endpoint.
// Field names mirror what kashalls/external-dns-unifi-webhook sends.
type DNSRecord struct {
	ID         string `json:"_id,omitempty"`
	Key        string `json:"key"`
	RecordType string `json:"record_type"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl,omitempty"`
	Enabled    bool   `json:"enabled"`
}

// listResponse wraps the slice that the GET endpoint returns.
type listResponse []DNSRecord
