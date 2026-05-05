package source

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const (
	labelTraefikEnable     = "traefik.enable"
	labelExternalDNSEnable = "external-dns.enabled"
	labelRouterRulePrefix  = "traefik.http.routers."
	labelRouterRuleSuffix  = ".rule"
)

// hostExtract matches Host(`foo.example.com`) entries in a Traefik rule string.
var hostExtract = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// EndpointsFromLabels parses a container's labels and returns the desired DNS
// endpoints. Returns nil if the container is not in scope (missing enable labels).
func EndpointsFromLabels(containerName string, labels map[string]string, targetIP, ownerID string) []*Endpoint {
	if !isTrue(labels[labelTraefikEnable]) || !isTrue(labels[labelExternalDNSEnable]) {
		return nil
	}

	var hostnames []string
	for key, val := range labels {
		if !strings.HasPrefix(key, labelRouterRulePrefix) || !strings.HasSuffix(key, labelRouterRuleSuffix) {
			continue
		}
		for _, match := range hostExtract.FindAllStringSubmatch(val, -1) {
			host := match[1]
			if strings.Contains(host, "${") {
				// compose-time variable not substituted — skip
				slog.Debug("skipping unresolved variable in host", "label", key, "value", host)
				continue
			}
			hostnames = append(hostnames, host)
		}
	}

	if len(hostnames) == 0 {
		slog.Debug("container has no extractable hostnames", "container", containerName)
		return nil
	}

	endpoints := make([]*Endpoint, 0, len(hostnames))
	for _, h := range hostnames {
		endpoints = append(endpoints, &Endpoint{
			DNSName:    h,
			Target:     targetIP,
			RecordType: "A",
			OwnerID:    ownerID,
			Resource:   fmt.Sprintf("docker/%s", containerName),
		})
	}
	return endpoints
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}
