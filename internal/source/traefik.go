package source

import (
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"sort"
	"strings"
)

const (
	labelExternalDNSEnable       = "external-dns.enabled"
	labelExternalDNSTarget       = "external-dns.target"
	labelExternalDNSRouterPrefix = "external-dns.routers."
	labelRouterRulePrefix        = "traefik.http.routers."
	labelRouterRuleSuffix        = ".rule"
)

// hostExtract matches Host(`foo.example.com`) entries in a Traefik rule string.
var hostExtract = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// detectRecordType returns "A" for IPv4 targets and "CNAME" for everything else.
func detectRecordType(target string) string {
	if ip := net.ParseIP(target); ip != nil && ip.To4() != nil {
		return "A"
	}
	return "CNAME"
}

// EndpointsFromLabels parses a container's labels and returns the desired DNS
// endpoints. Returns nil if the container is not in scope.
func EndpointsFromLabels(containerName string, labels map[string]string, defaultTarget, ownerID string) []*Endpoint {
	if !isTrue(labels[labelExternalDNSEnable]) {
		return nil
	}

	type routerOverride struct {
		target string
		skip   bool
	}

	containerTarget := labels[labelExternalDNSTarget]
	routerRules := make(map[string]string)
	routerOverrides := make(map[string]routerOverride)

	for key, val := range labels {
		if strings.HasPrefix(key, labelRouterRulePrefix) && strings.HasSuffix(key, labelRouterRuleSuffix) {
			name := strings.TrimSuffix(strings.TrimPrefix(key, labelRouterRulePrefix), labelRouterRuleSuffix)
			routerRules[name] = val
			continue
		}

		routerName, field, ok := parseRouterLabel(key)
		if !ok {
			continue
		}
		ro := routerOverrides[routerName]
		switch field {
		case "target":
			ro.target = val
		case "skip":
			ro.skip = isTrue(val)
		}
		routerOverrides[routerName] = ro
	}

	routerNames := make([]string, 0, len(routerRules))
	for name := range routerRules {
		routerNames = append(routerNames, name)
	}
	sort.Strings(routerNames)

	var endpoints []*Endpoint
	for _, routerName := range routerNames {
		rule := routerRules[routerName]
		ro := routerOverrides[routerName]
		if ro.skip {
			continue
		}

		target := defaultTarget
		if containerTarget != "" {
			target = containerTarget
		}
		if ro.target != "" {
			target = ro.target
		}

		recordType := detectRecordType(target)

		for _, match := range hostExtract.FindAllStringSubmatch(rule, -1) {
			host := match[1]
			if strings.Contains(host, "${") {
				// compose-time variable not substituted — skip
				slog.Debug("skipping unresolved variable in host", "router", routerName, "value", host)
				continue
			}
			endpoints = append(endpoints, &Endpoint{
				DNSName:    host,
				Target:     target,
				RecordType: recordType,
				OwnerID:    ownerID,
				Resource:   fmt.Sprintf("docker/%s", containerName),
			})
		}
	}

	if len(endpoints) == 0 {
		slog.Debug("container has no extractable hostnames", "container", containerName)
		return nil
	}

	return endpoints
}

func parseRouterLabel(key string) (name, field string, ok bool) {
	rest, ok := strings.CutPrefix(key, labelExternalDNSRouterPrefix)
	if !ok {
		return "", "", false
	}
	idx := strings.LastIndex(rest, ".")
	if idx < 0 {
		return "", "", false
	}
	name, field = rest[:idx], rest[idx+1:]
	if name == "" || field == "" {
		return "", "", false
	}
	switch field {
	case "target", "skip":
		return name, field, true
	default:
		return "", "", false
	}
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}
