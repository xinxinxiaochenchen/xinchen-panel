package subscriptionconfig

import (
	"bytes"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"unicode"
)

func surgeName(value string) error {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, ",=\r\n[]#;") {
		return errors.New("surge name contains a delimiter")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("surge name contains a control character")
		}
	}
	return nil
}

func surgePassword(value string) error {
	if value == "" {
		return errors.New("surge password is empty")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return errors.New("surge password contains an unsupported character")
		}
	}
	return nil
}

func surgeRule(rule Rule) (string, error) {
	value := matchValue(rule)
	if value == "" || strings.ContainsAny(value, ",\r\n") {
		return "", errors.New("surge rule contains a delimiter")
	}
	kind := map[string]string{"domain": "DOMAIN", "domain_suffix": "DOMAIN-SUFFIX", "geoip": "GEOIP"}[rule.MatchType]
	if rule.MatchType == "geoip" {
		value = strings.ToUpper(value)
	}
	if rule.MatchType == "ip" || rule.MatchType == "cidr" {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", fmt.Errorf("invalid IP prefix: %w", err)
		}
		if prefix.Addr().Is6() {
			kind = "IP-CIDR6"
		} else {
			kind = "IP-CIDR"
		}
	}
	return kind + "," + value + "," + mihomoAction(rule.Action), nil
}

func renderSurge(targets []Target, names []string, policy *RoutingPolicy) ([]byte, error) {
	for i, target := range targets {
		if err := surgeName(names[i]); err != nil {
			return nil, fmt.Errorf("target %d: %w", i+1, err)
		}
		if err := surgePassword(target.Password); err != nil {
			return nil, fmt.Errorf("target %d: %w", i+1, err)
		}
		if policy != nil {
			if err := surgeName(lineTag(target.LineID)); err != nil {
				return nil, fmt.Errorf("target %d line: %w", i+1, err)
			}
		}
	}
	var b bytes.Buffer
	b.WriteString("[General]\nhttp-listen = 127.0.0.1:6152\nsocks5-listen = 127.0.0.1:6153\n\n[Proxy]\n")
	for i, target := range targets {
		server := target.Server
		if addr, err := netip.ParseAddr(server); err == nil && addr.Is6() {
			server = "[" + server + "]"
		}
		fmt.Fprintf(&b, "%s = trojan, %s, %d, password=%s, sni=%s, skip-cert-verify=false\n", names[i], server, target.Port, target.Password, target.ServerName)
	}
	b.WriteString("\n[Proxy Group]\nPROXY = select")
	for _, name := range names {
		fmt.Fprintf(&b, ", %s", name)
	}
	b.WriteByte('\n')
	if policy != nil {
		for _, line := range policyLines(targets, policy) {
			fmt.Fprintf(&b, "%s = select", lineTag(line))
			for i, target := range targets {
				if target.LineID == line {
					fmt.Fprintf(&b, ", %s", names[i])
				}
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("\n[Rule]\n")
	if policy != nil {
		for _, rule := range policy.Rules {
			rendered, err := surgeRule(rule)
			if err != nil {
				return nil, err
			}
			b.WriteString(rendered)
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "FINAL,%s\n", mihomoAction(policy.Fallback))
	} else {
		b.WriteString("FINAL,PROXY\n")
	}
	return b.Bytes(), nil
}
