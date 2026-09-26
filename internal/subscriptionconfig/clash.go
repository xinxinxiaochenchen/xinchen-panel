package subscriptionconfig

import (
	"bytes"
	"fmt"
)

// renderClash emits the fields supported by the classic Clash YAML profile.
// The Mihomo profile can use a mixed listener and an explicit TCP network;
// classic Clash uses separate local HTTP and SOCKS listeners and the default
// Trojan TCP transport.
func renderClash(targets []Target, names []string, policy *RoutingPolicy) ([]byte, error) {
	if policy != nil {
		for _, target := range targets {
			if err := surgeName(lineTag(target.LineID)); err != nil {
				return nil, fmt.Errorf("invalid Clash line ID: %w", err)
			}
		}
	}
	var b bytes.Buffer
	b.WriteString("port: 7890\nsocks-port: 7891\nallow-lan: false\nbind-address: \"127.0.0.1\"\nmode: rule\nproxies:\n")
	for i, target := range targets {
		fmt.Fprintf(&b, "  - name: %s\n    type: trojan\n    server: %s\n    port: %d\n    password: %s\n    sni: %s\n    udp: false\n    skip-cert-verify: false\n",
			yamlString(names[i]), yamlString(target.Server), target.Port,
			yamlString(target.Password), yamlString(target.ServerName))
	}
	b.WriteString("proxy-groups:\n  - name: \"PROXY\"\n    type: select\n    proxies:\n")
	for _, name := range names {
		fmt.Fprintf(&b, "      - %s\n", yamlString(name))
	}
	if policy != nil {
		for _, line := range policyLines(targets, policy) {
			fmt.Fprintf(&b, "  - name: %s\n    type: select\n    proxies:\n", yamlString(lineTag(line)))
			for i, target := range targets {
				if target.LineID == line {
					fmt.Fprintf(&b, "      - %s\n", yamlString(names[i]))
				}
			}
		}
	}
	b.WriteString("rules:\n")
	if policy != nil {
		for _, rule := range policy.Rules {
			rendered, err := surgeRule(rule)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "  - %s\n", yamlString(rendered))
		}
		fmt.Fprintf(&b, "  - %s\n", yamlString("MATCH,"+mihomoAction(policy.Fallback)))
	} else {
		b.WriteString("  - \"MATCH,PROXY\"\n")
	}
	return b.Bytes(), nil
}
