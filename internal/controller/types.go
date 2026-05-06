package controller

import (
	"context"

	"github.com/movishell/docker-external-dns/internal/provider/unifi"
	"github.com/movishell/docker-external-dns/internal/source"
)

// Event is a lightweight, source-agnostic notification that desired
// state may have changed. The controller uses it as a debounce trigger
// and to log which container caused the change.
type Event struct {
	Action string
	Name   string
}

// Source supplies desired endpoints and a stream of change notifications.
type Source interface {
	Endpoints(ctx context.Context) ([]*source.Endpoint, error)
	Events(ctx context.Context) (<-chan Event, <-chan error)
}

// Provider is the DNS backend the controller drives.
type Provider interface {
	ListRecords(ctx context.Context) ([]unifi.DNSRecord, error)
	CreateRecord(ctx context.Context, r unifi.DNSRecord) (unifi.DNSRecord, error)
	UpdateRecord(ctx context.Context, r unifi.DNSRecord) (unifi.DNSRecord, error)
	DeleteRecord(ctx context.Context, id, key, recordType string) error
}
