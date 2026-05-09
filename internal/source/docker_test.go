package source

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
)

// ---- fake Docker client ----

type fakeDockerClient struct {
	containers        []container.Summary
	listErr           error
	eventCh           chan events.Message
	errCh             chan error
	lastEventsOptions events.ListOptions
}

func (f *fakeDockerClient) ContainerList(_ context.Context, _ container.ListOptions) ([]container.Summary, error) {
	return f.containers, f.listErr
}

func (f *fakeDockerClient) Events(_ context.Context, opts events.ListOptions) (<-chan events.Message, <-chan error) {
	f.lastEventsOptions = opts
	if f.eventCh == nil {
		ch := make(chan events.Message)
		errCh := make(chan error)
		close(ch)
		close(errCh)
		return ch, errCh
	}
	return f.eventCh, f.errCh
}

func (f *fakeDockerClient) Close() error { return nil }

// ---- helpers ----

func inScopeContainer(id, name string, extraLabels map[string]string) container.Summary {
	labels := map[string]string{
		"traefik.enable":       "true",
		"external-dns.enabled": "true",
	}
	for k, v := range extraLabels {
		labels[k] = v
	}
	return container.Summary{
		ID:     id,
		Names:  []string{"/" + name},
		Labels: labels,
	}
}

func newTestSource(cli *fakeDockerClient) *DockerSource {
	return newDockerSourceWithClient(cli, "10.0.0.1", "test-owner")
}

func dnsNames(eps []*Endpoint) []string {
	names := make([]string, len(eps))
	for i, ep := range eps {
		names[i] = ep.DNSName
	}
	sort.Strings(names)
	return names
}

// ---- tests ----

func TestEndpoints_EmptyContainerList(t *testing.T) {
	src := newTestSource(&fakeDockerClient{})
	eps, err := src.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 0 {
		t.Errorf("expected no endpoints, got %v", eps)
	}
}

func TestEndpoints_PropagatesListError(t *testing.T) {
	src := newTestSource(&fakeDockerClient{listErr: errors.New("daemon unavailable")})
	eps, err := src.Endpoints(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if eps != nil {
		t.Errorf("expected nil endpoints on error, got %v", eps)
	}
}

func TestEndpoints_AggregatesAcrossContainers(t *testing.T) {
	cli := &fakeDockerClient{
		containers: []container.Summary{
			inScopeContainer("id1", "svc-a", map[string]string{
				"traefik.http.routers.a.rule": "Host(`a.example.com`)",
			}),
			inScopeContainer("id2", "svc-b", map[string]string{
				"traefik.http.routers.b.rule": "Host(`b.example.com`)",
			}),
			// out-of-scope: missing external-dns label
			{ID: "id3", Names: []string{"/svc-c"}, Labels: map[string]string{
				"traefik.enable":              "true",
				"traefik.http.routers.c.rule": "Host(`c.example.com`)",
			}},
		},
	}
	src := newTestSource(cli)
	eps, err := src.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := dnsNames(eps)
	want := []string{"a.example.com", "b.example.com"}
	if !equalSlices(got, want) {
		t.Errorf("dns names = %v, want %v", got, want)
	}
}

func TestEndpoints_StripsLeadingSlashFromName(t *testing.T) {
	cli := &fakeDockerClient{
		containers: []container.Summary{
			inScopeContainer("id1", "whoami", map[string]string{
				"traefik.http.routers.w.rule": "Host(`whoami.example.com`)",
			}),
		},
	}
	src := newTestSource(cli)
	eps, err := src.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].Resource != "docker/whoami" {
		t.Errorf("Resource = %q, want %q", eps[0].Resource, "docker/whoami")
	}
}

func TestEndpoints_FallsBackToIDWhenNamesEmpty(t *testing.T) {
	cli := &fakeDockerClient{
		containers: []container.Summary{
			{
				ID:    "abcdef0123456789",
				Names: nil,
				Labels: map[string]string{
					"traefik.enable":              "true",
					"external-dns.enabled":        "true",
					"traefik.http.routers.x.rule": "Host(`x.example.com`)",
				},
			},
		},
	}
	src := newTestSource(cli)
	eps, err := src.Endpoints(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(eps))
	}
	if eps[0].Resource != "docker/abcdef012345" {
		t.Errorf("Resource = %q, want %q", eps[0].Resource, "docker/abcdef012345")
	}
}

func TestEvents_FilterIncludesContainerLifecycleEvents(t *testing.T) {
	cli := &fakeDockerClient{}
	src := newTestSource(cli)
	src.Events(context.Background())

	f := cli.lastEventsOptions.Filters

	if types := f.Get("type"); len(types) != 1 || types[0] != "container" {
		t.Errorf("filter type = %v, want [container]", types)
	}

	wantEvents := map[string]bool{"start": true, "die": true, "destroy": true, "update": true}
	for _, ev := range f.Get("event") {
		delete(wantEvents, ev)
	}
	if len(wantEvents) > 0 {
		t.Errorf("missing event filters: %v", wantEvents)
	}
}

func TestEvents_PassesThroughChannels(t *testing.T) {
	evCh := make(chan events.Message, 1)
	errCh := make(chan error)
	evCh <- events.Message{Action: "start"}

	cli := &fakeDockerClient{eventCh: evCh, errCh: errCh}
	src := newTestSource(cli)

	gotEv, _ := src.Events(context.Background())
	msg := <-gotEv
	if msg.Action != "start" {
		t.Errorf("Action = %q, want %q", msg.Action, "start")
	}
}
