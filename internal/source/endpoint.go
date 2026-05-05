package source

// Endpoint represents a desired DNS record derived from a container's labels.
type Endpoint struct {
	// DNSName is the fully-qualified hostname (e.g. "foo.example.com").
	DNSName string
	// Target is the IP address for A records.
	Target string
	// RecordType is always "A" for now.
	RecordType string
	// OwnerID identifies the agent instance that owns this record.
	OwnerID string
	// Resource is a back-reference to the source container, used in TXT ownership records.
	// Format: "docker/<container-name>"
	Resource string
}
