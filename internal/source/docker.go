package source

import (
	"context"
	"log/slog"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

// dockerClient is the subset of *client.Client that DockerSource uses.
// Defined here so tests can supply a fake without a live daemon.
type dockerClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
	Close() error
}

// DockerSource lists running containers and streams lifecycle events.
type DockerSource struct {
	cli           dockerClient
	defaultTarget string
	ownerID       string
}

func NewDockerSource(dockerHost, defaultTarget, ownerID string) (*DockerSource, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &DockerSource{cli: cli, defaultTarget: defaultTarget, ownerID: ownerID}, nil
}

// newDockerSourceWithClient constructs a DockerSource using an injected client.
// Used by tests to supply a fake without a live Docker daemon.
func newDockerSourceWithClient(cli dockerClient, defaultTarget, ownerID string) *DockerSource {
	return &DockerSource{cli: cli, defaultTarget: defaultTarget, ownerID: ownerID}
}

// Endpoints returns the desired DNS endpoints from all currently running containers.
func (s *DockerSource) Endpoints(ctx context.Context) ([]*Endpoint, error) {
	containers, err := s.cli.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		return nil, err
	}

	sort.Slice(containers, func(i, j int) bool {
		return containerName(containers[i]) < containerName(containers[j])
	})

	var all []*Endpoint
	for _, c := range containers {
		name := containerName(c)
		eps := EndpointsFromLabels(name, c.Labels, s.defaultTarget, s.ownerID)
		if len(eps) > 0 {
			slog.Debug("found endpoints for container", "container", name, "count", len(eps))
		}
		all = append(all, eps...)
	}
	return all, nil
}

// Events returns channels for relevant Docker lifecycle events and errors.
// The caller must drain both channels until ctx is cancelled.
func (s *DockerSource) Events(ctx context.Context) (<-chan events.Message, <-chan error) {
	f := filters.NewArgs()
	f.Add("type", "container")
	f.Add("event", "start")
	f.Add("event", "die")
	f.Add("event", "destroy")
	f.Add("event", "update")

	return s.cli.Events(ctx, events.ListOptions{Filters: f})
}

// Close releases the Docker client.
func (s *DockerSource) Close() error {
	return s.cli.Close()
}

func containerName(c container.Summary) string {
	if len(c.Names) > 0 {
		// Docker prefixes names with "/"
		return strings.TrimPrefix(c.Names[0], "/")
	}
	return c.ID[:12]
}
