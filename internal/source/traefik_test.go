package source

import (
	"sort"
	"testing"
)

type endpointWant struct {
	DNSName    string
	Target     string
	RecordType string
}

func TestEndpointsFromLabels(t *testing.T) {
	const defaultTarget = "10.1.2.241"
	const ownerID = "test-owner"

	tests := []struct {
		name      string
		container string
		labels    map[string]string
		want      []endpointWant
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
				"traefik.enable":                "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: nil,
		},
		{
			name:      "only legacy external-dns enable",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: defaultTarget, RecordType: "A"}},
		},
		{
			name:      "only dexd enable",
			container: "c1",
			labels: map[string]string{
				"dexd.enabled":                  "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: defaultTarget, RecordType: "A"}},
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
			want: []endpointWant{{DNSName: "foo.example.com", Target: defaultTarget, RecordType: "A"}},
		},
		{
			name:      "Host || Host",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`a.example.com`) || Host(`b.example.com`)",
			},
			want: []endpointWant{
				{DNSName: "a.example.com", Target: defaultTarget, RecordType: "A"},
				{DNSName: "b.example.com", Target: defaultTarget, RecordType: "A"},
			},
		},
		{
			name:      "HostRegexp is skipped, Host is kept",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "HostRegexp(`^.+\\.example\\.com$`) || Host(`a.example.com`)",
			},
			want: []endpointWant{{DNSName: "a.example.com", Target: defaultTarget, RecordType: "A"}},
		},
		{
			name:      "unsubstituted variable is skipped",
			container: "c1",
			labels: map[string]string{
				"traefik.enable":                "true",
				"external-dns.enabled":          "true",
				"traefik.http.routers.foo.rule": "Host(`${HOST}`) || Host(`real.example.com`)",
			},
			want: []endpointWant{{DNSName: "real.example.com", Target: defaultTarget, RecordType: "A"}},
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
			want: []endpointWant{
				{DNSName: "bar.example.com", Target: defaultTarget, RecordType: "A"},
				{DNSName: "foo.example.com", Target: defaultTarget, RecordType: "A"},
			},
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
		{
			name:      "container target overrides default",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":          "true",
				"external-dns.target":           "10.9.8.7",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: "10.9.8.7", RecordType: "A"}},
		},
		{
			name:      "hostname target auto-detects as CNAME",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":          "true",
				"external-dns.target":           "traefik.example.com",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: "traefik.example.com", RecordType: "CNAME"}},
		},
		{
			name:      "dexd target beats legacy target",
			container: "c1",
			labels: map[string]string{
				"dexd.enabled":                  "true",
				"dexd.target":                   "10.8.8.8",
				"external-dns.target":           "10.9.8.7",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: "10.8.8.8", RecordType: "A"}},
		},
		{
			name:      "router target beats container and default",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":            "true",
				"external-dns.target":             "10.9.8.7",
				"external-dns.routers.foo.target": "10.1.1.9",
				"traefik.http.routers.foo.rule":   "Host(`foo.example.com`)",
				"traefik.http.routers.bar.rule":   "Host(`bar.example.com`)",
			},
			want: []endpointWant{
				{DNSName: "bar.example.com", Target: "10.9.8.7", RecordType: "A"},
				{DNSName: "foo.example.com", Target: "10.1.1.9", RecordType: "A"},
			},
		},
		{
			name:      "dexd router target beats legacy router target",
			container: "c1",
			labels: map[string]string{
				"dexd.enabled":                    "true",
				"dexd.routers.foo.target":         "10.8.8.8",
				"external-dns.routers.foo.target": "10.9.8.7",
				"traefik.http.routers.foo.rule":   "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: "10.8.8.8", RecordType: "A"}},
		},
		{
			name:      "router override supports dotted router names",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":                "true",
				"external-dns.routers.foo.bar.target": "10.1.1.9",
				"traefik.http.routers.foo.bar.rule":   "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: "10.1.1.9", RecordType: "A"}},
		},
		{
			name:      "hostname router target auto-detects as CNAME",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":            "true",
				"external-dns.routers.foo.target": "traefik.example.com",
				"traefik.http.routers.foo.rule":   "Host(`foo.example.com`)",
				"traefik.http.routers.bar.rule":   "Host(`bar.example.com`)",
			},
			want: []endpointWant{
				{DNSName: "bar.example.com", Target: defaultTarget, RecordType: "A"},
				{DNSName: "foo.example.com", Target: "traefik.example.com", RecordType: "CNAME"},
			},
		},
		{
			name:      "router skip drops matching router",
			container: "c1",
			labels: map[string]string{
				"external-dns.enabled":          "true",
				"external-dns.routers.foo.skip": "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
				"traefik.http.routers.bar.rule": "Host(`bar.example.com`)",
			},
			want: []endpointWant{{DNSName: "bar.example.com", Target: defaultTarget, RecordType: "A"}},
		},
		{
			name:      "dexd router skip beats legacy router skip",
			container: "c1",
			labels: map[string]string{
				"dexd.enabled":                  "true",
				"dexd.routers.foo.skip":         "false",
				"external-dns.routers.foo.skip": "true",
				"traefik.http.routers.foo.rule": "Host(`foo.example.com`)",
			},
			want: []endpointWant{{DNSName: "foo.example.com", Target: defaultTarget, RecordType: "A"}},
		},
		{
			name:      "rustfs style mixed routers: IP default + hostname override",
			container: "rustfs",
			labels: map[string]string{
				"external-dns.enabled":                "true",
				"external-dns.routers.console.target": "traefik.example.com",
				"traefik.http.routers.s3.rule":        "Host(`bucket.example.com`)",
				"traefik.http.routers.console.rule":   "Host(`console.example.com`)",
			},
			want: []endpointWant{
				{DNSName: "bucket.example.com", Target: defaultTarget, RecordType: "A"},
				{DNSName: "console.example.com", Target: "traefik.example.com", RecordType: "CNAME"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EndpointsFromLabels(tt.container, tt.labels, defaultTarget, ownerID)

			gotEndpoints := make([]endpointWant, len(got))
			for i, ep := range got {
				gotEndpoints[i] = endpointWant{DNSName: ep.DNSName, Target: ep.Target, RecordType: ep.RecordType}
				if ep.OwnerID != ownerID {
					t.Errorf("endpoint %s: OwnerID = %q, want %q", ep.DNSName, ep.OwnerID, ownerID)
				}
				wantResource := "docker/" + tt.container
				if ep.Resource != wantResource {
					t.Errorf("endpoint %s: Resource = %q, want %q", ep.DNSName, ep.Resource, wantResource)
				}
			}
			sortEndpoints(gotEndpoints)
			sortEndpoints(tt.want)

			if !equalEndpoints(gotEndpoints, tt.want) {
				t.Errorf("Endpoints = %v, want %v", gotEndpoints, tt.want)
			}
		})
	}
}

func sortEndpoints(eps []endpointWant) {
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].DNSName != eps[j].DNSName {
			return eps[i].DNSName < eps[j].DNSName
		}
		return eps[i].RecordType < eps[j].RecordType
	})
}

func equalEndpoints(a, b []endpointWant) bool {
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

func TestDetectRecordType(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"10.0.0.1", "A"},
		{"192.168.1.254", "A"},
		{"10.1.2.241", "A"},
		{"traefik.example.com", "CNAME"},
		{"my-host.internal", "CNAME"},
		{"::1", "CNAME"},         // IPv6 falls through to CNAME (AAAA not supported yet)
		{"2001:db8::1", "CNAME"}, // IPv6 falls through to CNAME
		{"", "CNAME"},            // empty string → CNAME
		{"not-an-ip.foo", "CNAME"},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			got := detectRecordType(tt.target)
			if got != tt.want {
				t.Errorf("detectRecordType(%q) = %q, want %q", tt.target, got, tt.want)
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
