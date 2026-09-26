// Package subscriptionconfig renders self-contained client profiles from
// already-authorized proxy targets. It performs no persistence or entitlement work.
package subscriptionconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const defaultTemplate = "{region} · {name}"

type Target struct {
	ID, Name, LineID, LineName, Region, Server, ServerName, Password string
	Port                                                             int
}

func ValidateTemplate(template string) error {
	if !utf8.ValidString(template) {
		return errors.New("name template is not valid UTF-8")
	}
	if template == "" {
		return nil
	}
	if strings.TrimSpace(template) == "" {
		return errors.New("name template is blank")
	}
	if utf8.RuneCountInString(template) > 200 {
		return errors.New("name template is too long")
	}
	for _, r := range template {
		if unicode.IsControl(r) {
			return errors.New("name template contains control characters")
		}
	}
	maxExpanded := 0
	for i := 0; i < len(template); {
		switch template[i] {
		case '{':
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return errors.New("unclosed template placeholder")
			}
			name := template[i+1 : i+1+end]
			switch name {
			case "name", "line", "region", "index":
			default:
				return fmt.Errorf("unknown template placeholder %q", name)
			}
			if name == "index" {
				maxExpanded += 3
			} else {
				maxExpanded += 100
			}
			i += end + 2
		case '}':
			return errors.New("unmatched template closing brace")
		default:
			_, size := utf8.DecodeRuneInString(template[i:])
			i += size
			maxExpanded++
		}
	}
	if maxExpanded > 1024 {
		return errors.New("name template can expand beyond 1024 characters")
	}
	return nil
}

func Render(format, template string, targets []Target) ([]byte, string, error) {
	return RenderWithRouting(format, template, targets, nil)
}

func RenderWithRouting(format, template string, targets []Target, policy *RoutingPolicy) ([]byte, string, error) {
	if format != "mihomo" && format != "sing-box" {
		return nil, "", fmt.Errorf("unsupported subscription format %q", format)
	}
	if template == "" {
		template = defaultTemplate
	}
	if err := ValidateTemplate(template); err != nil {
		return nil, "", err
	}
	if len(targets) == 0 {
		return nil, "", errors.New("subscription has no targets")
	}
	names := make([]string, len(targets))
	used := map[string]bool{"PROXY": true, "DIRECT": true}
	if policy != nil {
		for _, target := range targets {
			used[strings.ToUpper(lineTag(target.LineID))] = true
		}
	}
	for i, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, "", fmt.Errorf("target %d: %w", i+1, err)
		}
		base := strings.NewReplacer(
			"{name}", target.Name,
			"{line}", target.LineName,
			"{region}", target.Region,
			"{index}", strconv.Itoa(i+1),
		).Replace(template)
		if strings.TrimSpace(base) == "" {
			return nil, "", fmt.Errorf("target %d: empty rendered name", i+1)
		}
		if utf8.RuneCountInString(base) > 1024 {
			return nil, "", fmt.Errorf("target %d: rendered name is too long", i+1)
		}
		name := base
		for n := 2; used[strings.ToUpper(name)]; n++ {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		if utf8.RuneCountInString(name) > 1024 {
			return nil, "", fmt.Errorf("target %d: rendered name is too long", i+1)
		}
		used[strings.ToUpper(name)] = true
		names[i] = name
	}
	if err := validateRouting(format, targets, policy); err != nil {
		return nil, "", err
	}
	if format == "mihomo" {
		return renderMihomo(targets, names, policy), "application/yaml", nil
	}
	data, err := renderSingBox(targets, names, policy)
	if err != nil {
		return nil, "", err
	}
	return data, "application/json", nil
}

func validateTarget(target Target) error {
	if !utf8.ValidString(target.ID) || strings.TrimSpace(target.ID) == "" || len(target.ID) > 100 {
		return errors.New("invalid ID")
	}
	if strings.TrimSpace(target.Name) == "" {
		return errors.New("invalid name")
	}
	for _, field := range []struct{ name, value string }{
		{"name", target.Name}, {"line name", target.LineName}, {"region", target.Region},
	} {
		if !utf8.ValidString(field.value) || utf8.RuneCountInString(field.value) > 100 {
			return fmt.Errorf("%s is too long", field.name)
		}
		for _, r := range field.value {
			if unicode.IsControl(r) {
				return fmt.Errorf("%s contains control characters", field.name)
			}
		}
	}
	if !validHost(target.Server) || !validHost(target.ServerName) {
		return errors.New("invalid server or TLS server name")
	}
	if target.Port < 1 || target.Port > 65535 {
		return errors.New("invalid server port")
	}
	if !utf8.ValidString(target.Password) || target.Password == "" || len(target.Password) > 4096 {
		return errors.New("invalid password")
	}
	return nil
}

func validHost(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.TrimSpace(host) != host {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// JSON strings are valid YAML double-quoted scalars and keep arbitrary values
// from adding YAML keys, rules, or proxy entries.
func yamlString(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func renderMihomo(targets []Target, names []string, policy *RoutingPolicy) []byte {
	var b bytes.Buffer
	b.WriteString("mixed-port: 7890\nallow-lan: false\nbind-address: \"127.0.0.1\"\nmode: rule\nproxies:\n")
	for i, target := range targets {
		fmt.Fprintf(&b, "  - name: %s\n    type: trojan\n    server: %s\n    port: %d\n    password: %s\n    sni: %s\n    network: tcp\n    udp: false\n    skip-cert-verify: false\n",
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
			fmt.Fprintf(&b, "  - %s\n", yamlString(mihomoRule(rule)))
		}
		fmt.Fprintf(&b, "  - %s\n", yamlString("MATCH,"+mihomoAction(policy.Fallback)))
	} else {
		b.WriteString("  - \"MATCH,PROXY\"\n")
	}
	return b.Bytes()
}

func renderSingBox(targets []Target, names []string, policy *RoutingPolicy) ([]byte, error) {
	type tlsConfig struct {
		Enabled    bool   `json:"enabled"`
		ServerName string `json:"server_name"`
		Insecure   bool   `json:"insecure"`
	}
	type outbound struct {
		Type       string     `json:"type"`
		Tag        string     `json:"tag"`
		Outbounds  []string   `json:"outbounds,omitempty"`
		Server     string     `json:"server,omitempty"`
		ServerPort int        `json:"server_port,omitempty"`
		Password   string     `json:"password,omitempty"`
		Network    string     `json:"network,omitempty"`
		TLS        *tlsConfig `json:"tls,omitempty"`
	}
	type dnsServer struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}
	cfg := struct {
		DNS struct {
			Servers []dnsServer `json:"servers"`
			Final   string      `json:"final"`
		} `json:"dns"`
		Inbounds []struct {
			Type       string `json:"type"`
			Tag        string `json:"tag"`
			Listen     string `json:"listen"`
			ListenPort int    `json:"listen_port"`
		} `json:"inbounds"`
		Outbounds []outbound `json:"outbounds"`
		Route     struct {
			Final                 string           `json:"final"`
			DefaultDomainResolver string           `json:"default_domain_resolver"`
			Rules                 []map[string]any `json:"rules,omitempty"`
		} `json:"route"`
	}{}
	cfg.DNS.Servers = []dnsServer{{Type: "local", Tag: "local-dns"}}
	cfg.DNS.Final = "local-dns"
	cfg.Inbounds = append(cfg.Inbounds, struct {
		Type       string `json:"type"`
		Tag        string `json:"tag"`
		Listen     string `json:"listen"`
		ListenPort int    `json:"listen_port"`
	}{Type: "mixed", Tag: "mixed-in", Listen: "127.0.0.1", ListenPort: 7890})
	cfg.Outbounds = append(cfg.Outbounds, outbound{Type: "selector", Tag: "PROXY", Outbounds: names})
	for i, target := range targets {
		cfg.Outbounds = append(cfg.Outbounds, outbound{
			Type: "trojan", Tag: names[i], Server: target.Server,
			ServerPort: target.Port, Password: target.Password, Network: "tcp",
			TLS: &tlsConfig{Enabled: true, ServerName: target.ServerName, Insecure: false},
		})
	}
	cfg.Route.Final = "PROXY"
	if policy != nil {
		cfg.Outbounds = append(cfg.Outbounds, outbound{Type: "direct", Tag: "DIRECT"})
		for _, line := range policyLines(targets, policy) {
			members := []string{}
			for i, target := range targets {
				if target.LineID == line {
					members = append(members, names[i])
				}
			}
			cfg.Outbounds = append(cfg.Outbounds, outbound{Type: "selector", Tag: lineTag(line), Outbounds: members})
		}
		for _, rule := range policy.Rules {
			cfg.Route.Rules = append(cfg.Route.Rules, singBoxRule(rule))
		}
		switch policy.Fallback.Kind {
		case "direct":
			cfg.Route.Final = "DIRECT"
		case "line":
			cfg.Route.Final = lineTag(policy.Fallback.LineID)
		case "block":
			cfg.Route.Rules = append(cfg.Route.Rules, map[string]any{"action": "reject"})
		}
	}
	cfg.Route.DefaultDomainResolver = "local-dns"
	return json.MarshalIndent(cfg, "", "  ")
}

type Action struct{ Kind, LineID string }
type Rule struct {
	MatchType, MatchValue string
	Action                Action
}
type RoutingPolicy struct {
	Fallback Action
	Rules    []Rule
}

func lineTag(line string) string { return "LINE:" + line }
func policyLines(targets []Target, policy *RoutingPolicy) []string {
	seen := map[string]bool{}
	for _, target := range targets {
		if target.LineID != "" {
			seen[target.LineID] = true
		}
	}
	for _, rule := range policy.Rules {
		if rule.Action.Kind == "line" {
			seen[rule.Action.LineID] = true
		}
	}
	if policy.Fallback.Kind == "line" {
		seen[policy.Fallback.LineID] = true
	}
	out := make([]string, 0, len(seen))
	for _, target := range targets {
		if target.LineID != "" && seen[target.LineID] {
			out = append(out, target.LineID)
			delete(seen, target.LineID)
		}
	}
	return out
}
func validateRouting(format string, targets []Target, policy *RoutingPolicy) error {
	if policy == nil {
		return nil
	}
	lines := map[string]bool{}
	for _, target := range targets {
		if target.LineID == "" {
			return errors.New("routing target has no line ID")
		}
		lines[target.LineID] = true
	}
	check := func(a Action) error {
		if a.Kind != "direct" && a.Kind != "block" && a.Kind != "line" {
			return fmt.Errorf("unsupported routing action %q", a.Kind)
		}
		if a.Kind == "line" && (a.LineID == "" || !lines[a.LineID]) {
			return fmt.Errorf("routing line %q is not exported", a.LineID)
		}
		return nil
	}
	if err := check(policy.Fallback); err != nil {
		return err
	}
	for _, rule := range policy.Rules {
		if rule.MatchType == "geosite" {
			return errors.New("geosite requires a managed rule-set")
		}
		if format == "sing-box" && rule.MatchType == "geoip" {
			return errors.New("sing-box geoip requires a managed rule-set")
		}
		if _, ok := map[string]bool{"domain": true, "domain_suffix": true, "ip": true, "cidr": true, "geoip": true}[rule.MatchType]; !ok {
			return fmt.Errorf("unsupported routing match type %q", rule.MatchType)
		}
		if err := check(rule.Action); err != nil {
			return err
		}
	}
	return nil
}
func mihomoAction(a Action) string {
	switch a.Kind {
	case "direct":
		return "DIRECT"
	case "block":
		return "REJECT"
	default:
		return lineTag(a.LineID)
	}
}
func matchValue(r Rule) string {
	if r.MatchType != "ip" {
		return r.MatchValue
	}
	addr, err := netip.ParseAddr(r.MatchValue)
	if err != nil {
		return r.MatchValue
	}
	return netip.PrefixFrom(addr, addr.BitLen()).String()
}
func mihomoRule(r Rule) string {
	kind := map[string]string{"domain": "DOMAIN", "domain_suffix": "DOMAIN-SUFFIX", "ip": "IP-CIDR", "cidr": "IP-CIDR", "geoip": "GEOIP"}[r.MatchType]
	return kind + "," + matchValue(r) + "," + mihomoAction(r.Action)
}
func singBoxRule(r Rule) map[string]any {
	key := map[string]string{"domain": "domain", "domain_suffix": "domain_suffix", "ip": "ip_cidr", "cidr": "ip_cidr"}[r.MatchType]
	v := map[string]any{key: []string{matchValue(r)}}
	if r.Action.Kind == "block" {
		v["action"] = "reject"
	} else {
		v["action"] = "route"
		v["outbound"] = mihomoAction(r.Action)
	}
	return v
}
