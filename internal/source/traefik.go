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
	labelDexdEnable       = "dexd.enabled"
	labelDexdTarget       = "dexd.target"
	labelDexdRouterPrefix = "dexd.routers."

	labelLegacyExternalDNSEnable       = "external-dns.enabled"
	labelLegacyExternalDNSTarget       = "external-dns.target"
	labelLegacyExternalDNSRouterPrefix = "external-dns.routers."

	labelRouterRulePrefix = "traefik.http.routers."
	labelRouterRuleSuffix = ".rule"
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
	if !enabled(labels) {
		return nil
	}

	type routerOverride struct {
		target    string
		targetSet bool
		skip      bool
		skipSet   bool
	}

	containerTarget := firstLabelValue(labels, labelDexdTarget, labelLegacyExternalDNSTarget)
	routerRules := make(map[string]string)
	routerOverrides := make(map[string]routerOverride)

	for key, val := range labels {
		if strings.HasPrefix(key, labelRouterRulePrefix) && strings.HasSuffix(key, labelRouterRuleSuffix) {
			name := strings.TrimSuffix(strings.TrimPrefix(key, labelRouterRulePrefix), labelRouterRuleSuffix)
			routerRules[name] = val
			continue
		}

		routerName, field, legacy, ok := parseRouterLabel(key)
		if !ok {
			continue
		}
		if legacy {
			existing := routerOverrides[routerName]
			if (field == "target" && existing.targetSet) || (field == "skip" && existing.skipSet) {
				continue
			}
		}

		ro := routerOverrides[routerName]
		switch field {
		case "target":
			ro.target = val
			ro.targetSet = true
		case "skip":
			ro.skip = isTrue(val)
			ro.skipSet = true
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

func enabled(labels map[string]string) bool {
	if val, ok := labels[labelDexdEnable]; ok {
		return isTrue(val)
	}
	return isTrue(labels[labelLegacyExternalDNSEnable])
}

func firstLabelValue(labels map[string]string, keys ...string) string {
	for _, key := range keys {
		if val := labels[key]; val != "" {
			return val
		}
	}
	return ""
}

func parseRouterLabel(key string) (name, field string, legacy, ok bool) {
	rest, ok := strings.CutPrefix(key, labelDexdRouterPrefix)
	if !ok {
		rest, ok = strings.CutPrefix(key, labelLegacyExternalDNSRouterPrefix)
		legacy = ok
	}
	if !ok {
		return "", "", false, false
	}
	idx := strings.LastIndex(rest, ".")
	if idx < 0 {
		return "", "", false, false
	}
	name, field = rest[:idx], rest[idx+1:]
	if name == "" || field == "" {
		return "", "", false, false
	}
	switch field {
	case "target", "skip":
		return name, field, legacy, true
	default:
		return "", "", false, false
	}
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}
