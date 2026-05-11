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
	labelDexdHostsPrefix  = "dexd.hosts."

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
		target         string
		targetSet      bool
		hostnames      []string
		hostnamesSet   bool
		extraHostnames []string
		skip           bool
		skipSet        bool
	}
	type hostBlock struct {
		hostnames    []string
		hostnamesSet bool
		target       string
		targetSet    bool
		skip         bool
		skipSet      bool
	}

	containerTarget := firstLabelValue(labels, labelDexdTarget, labelLegacyExternalDNSTarget)
	routerRules := make(map[string]string)
	routerOverrides := make(map[string]routerOverride)
	hostBlocks := make(map[string]hostBlock)

	for key, val := range labels {
		if strings.HasPrefix(key, labelRouterRulePrefix) && strings.HasSuffix(key, labelRouterRuleSuffix) {
			name := strings.TrimSuffix(strings.TrimPrefix(key, labelRouterRulePrefix), labelRouterRuleSuffix)
			routerRules[name] = val
			continue
		}

		if routerName, field, legacy, ok := parseRouterLabel(key); ok {
			if legacy {
				existing := routerOverrides[routerName]
				if (field == "target" && existing.targetSet) ||
					(field == "skip" && existing.skipSet) ||
					field == "hostnames" ||
					field == "extra-hostnames" {
					continue
				}
			}

			ro := routerOverrides[routerName]
			switch field {
			case "target":
				ro.target = val
				ro.targetSet = true
			case "hostnames":
				ro.hostnames = parseHostnameList(val)
				ro.hostnamesSet = true
			case "extra-hostnames":
				ro.extraHostnames = parseHostnameList(val)
			case "skip":
				ro.skip = isTrue(val)
				ro.skipSet = true
			}
			routerOverrides[routerName] = ro
			continue
		}

		hostName, field, ok := parseNamedLabel(key, labelDexdHostsPrefix)
		if !ok {
			continue
		}
		block := hostBlocks[hostName]
		switch field {
		case "hostnames":
			block.hostnames = parseHostnameList(val)
			block.hostnamesSet = true
		case "target":
			block.target = val
			block.targetSet = true
		case "skip":
			block.skip = isTrue(val)
			block.skipSet = true
		default:
			continue
		}
		hostBlocks[hostName] = block
	}

	appendEndpoint := func(endpoints []*Endpoint, dnsName, target, recordType, resource string) []*Endpoint {
		if strings.Contains(dnsName, "${") {
			// compose-time variable not substituted — skip
			slog.Debug("skipping unresolved variable in hostname", "value", dnsName)
			return endpoints
		}
		return append(endpoints, &Endpoint{
			DNSName:    dnsName,
			Target:     target,
			RecordType: recordType,
			OwnerID:    ownerID,
			Resource:   resource,
		})
	}

	var endpoints []*Endpoint

	hostBlockNames := make([]string, 0, len(hostBlocks))
	for name := range hostBlocks {
		hostBlockNames = append(hostBlockNames, name)
	}
	sort.Strings(hostBlockNames)

	for _, blockName := range hostBlockNames {
		block := hostBlocks[blockName]
		if block.skip {
			continue
		}
		if !block.hostnamesSet || len(block.hostnames) == 0 {
			slog.Debug("standalone host block has no hostnames", "block", blockName, "container", containerName)
			continue
		}

		target := defaultTarget
		if containerTarget != "" {
			target = containerTarget
		}
		if block.target != "" {
			target = block.target
		}
		recordType := detectRecordType(target)
		resource := fmt.Sprintf("docker/%s/hosts/%s", containerName, blockName)
		for _, host := range block.hostnames {
			endpoints = appendEndpoint(endpoints, host, target, recordType, resource)
		}
	}

	for _, routerName := range sortedKeys(routerRules) {
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
		hostnames := ro.hostnames
		if !ro.hostnamesSet {
			hostnames = hostnamesFromRule(rule)
		}
		hostnames = append(hostnames, ro.extraHostnames...)

		for _, host := range uniqueStrings(hostnames) {
			endpoints = appendEndpoint(endpoints, host, target, recordType, fmt.Sprintf("docker/%s", containerName))
		}
	}

	if len(endpoints) == 0 {
		slog.Debug("container has no extractable hostnames", "container", containerName)
		return nil
	}

	return endpoints
}

func hostnamesFromRule(rule string) []string {
	var hostnames []string
	for _, match := range hostExtract.FindAllStringSubmatch(rule, -1) {
		hostnames = append(hostnames, match[1])
	}
	return hostnames
}

func parseHostnameList(value string) []string {
	var hostnames []string
	for _, raw := range strings.Split(value, ",") {
		hostname := strings.TrimSpace(raw)
		if hostname != "" {
			hostnames = append(hostnames, hostname)
		}
	}
	return hostnames
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
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
	name, field, ok = parseNamedLabel(key, labelDexdRouterPrefix)
	if !ok {
		name, field, ok = parseNamedLabel(key, labelLegacyExternalDNSRouterPrefix)
		legacy = ok
	}
	return name, field, legacy, ok
}

func parseNamedLabel(key, prefix string) (name, field string, ok bool) {
	rest, ok := strings.CutPrefix(key, prefix)
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
	case "target", "skip", "hostnames", "extra-hostnames":
		return name, field, true
	default:
		return "", "", false
	}
}

func isTrue(v string) bool {
	return strings.EqualFold(v, "true") || v == "1"
}
