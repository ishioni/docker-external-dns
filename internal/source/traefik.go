package source

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

const (
	labelExternalDNSEnable       = "external-dns.enabled"
	labelExternalDNSTarget       = "external-dns.target"
	labelExternalDNSRecordType   = "external-dns.record-type"
	labelExternalDNSRouterPrefix = "external-dns.routers."
	labelRouterRulePrefix        = "traefik.http.routers."
	labelRouterRuleSuffix        = ".rule"
	defaultRecordType            = "A"
)

// hostExtract matches Host(`foo.example.com`) entries in a Traefik rule string.
var hostExtract = regexp.MustCompile("Host\\(`([^`]+)`\\)")

// EndpointsFromLabels parses a container's labels and returns the desired DNS
// endpoints. Returns nil if the container is not in scope.
func EndpointsFromLabels(containerName string, labels map[string]string, defaultTarget, ownerID string) []*Endpoint {
	if !isTrue(labels[labelExternalDNSEnable]) {
		return nil
	}

	type routerOverride struct {
		target     string
		recordType string
		skip       bool
	}

	containerTarget := labels[labelExternalDNSTarget]
	containerRecordType := labels[labelExternalDNSRecordType]
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
		case "record-type":
			ro.recordType = val
		case "skip":
			ro.skip = isTrue(val)
		}
		routerOverrides[routerName] = ro
	}

	var endpoints []*Endpoint
	for routerName, rule := range routerRules {
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

		recordType := defaultRecordType
		if containerRecordType != "" {
			recordType = strings.ToUpper(containerRecordType)
		}
		if ro.recordType != "" {
			recordType = strings.ToUpper(ro.recordType)
		}

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
	case "target", "record-type", "skip":
		return name, field, true
	default:
		return "", "", false
	}
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}
