package source

import (
	"sort"
	"testing"
)

func TestEndpointsFromLabels(t *testing.T) {
	const targetIP = "10.1.2.241"
	const ownerID = "test-owner"

	tests := []struct {
		name      string
		container string
		labels    map[string]string
		want      []string // expected DNSNames, sorted
	}{
		{
			name:      "no labels",
			container: "c1",
			labels:    map[string]string{},
			want:      nil,
		},
		{
			name:      "only traefik enable",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                            "true",
				"traefik.http.routers.foo.rule":             "Host(`foo.example.com`)",
			},
			want: nil,
		},
		{
			name:      "only external-dns enable",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":              "true",
				"traefik.http.routers.foo.rule":     "Host(`foo.example.com`)",
			},
			want: nil,
		},
		{
			name:      "both enabled but no router rules",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":       "true",
				"external-dns.enabled": "true",
			},
			want: nil,
		},
		{
			name:      "single Host",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []string{"foo.example.com"},
		},
		{
			name:      "Host || Host",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`a.example.com`) || Host(`b.example.com`)",
			},
			want: []string{"a.example.com", "b.example.com"},
		},
		{
			name:      "HostRegexp is skipped, Host is kept",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "HostRegexp(`^.+\\.example\\.com$`) || Host(`a.example.com`)",
			},
			want: []string{"a.example.com"},
		},
		{
			name:      "unsubstituted variable is skipped",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`${HOST}`) || Host(`real.example.com`)",
			},
			want: []string{"real.example.com"},
		},
		{
			name:      "multiple routers",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
				"traefik.http.routers.bar.rule": "Host(`bar.example.com`)",
			},
			want: []string{"bar.example.com", "foo.example.com"},
		},
		{
			name:      "external-dns false still skipped",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "false",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EndpointsFromLabels(tt.container, tt.labels, targetIP, ownerID)

			gotNames := make([]string, len(got))
			for i, ep := range got {
				gotNames[i] = ep.DNSName
				if ep.Target != targetIP {
					t.Errorf("endpoint %s: Target = %q, want %q", ep.DNSName, ep.Target, targetIP)
				}
				if ep.OwnerID != ownerID {
					t.Errorf("endpoint %s: OwnerID = %q, want %q", ep.DNSName, ep.OwnerID, ownerID)
				}
				if ep.RecordType != "A" {
					t.Errorf("endpoint %s: RecordType = %q, want A", ep.DNSName, ep.RecordType)
				}
				wantResource := "docker/" + tt.container
				if ep.Resource != wantResource {
					t.Errorf("endpoint %s: Resource = %q, want %q", ep.DNSName, ep.Resource, wantResource)
				}
			}
			sort.Strings(gotNames)

			if !equalSlices(gotNames, tt.want) {
				t.Errorf("DNSNames = %v, want %v", gotNames, tt.want)
			}
		})
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
