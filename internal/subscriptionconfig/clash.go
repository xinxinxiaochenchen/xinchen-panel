package subscriptionconfig

import (
	"bytes"
	"fmt"
)

// renderClash emits the fields supported by the classic Clash YAML profile.
// The Mihomo profile can use a mixed listener and an explicit TCP network;
// classic Clash uses separate local HTTP and SOCKS listeners and the default
// Trojan TCP transport.
func renderClash(targets []Target, names []string) ([]byte, error) {
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
	b.WriteString("rules:\n  - \"MATCH,PROXY\"\n")
	return b.Bytes(), nil
}
