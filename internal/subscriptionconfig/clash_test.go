package subscriptionconfig

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderClashTrojanAndRouting(t *testing.T) {
	jp := target("Japan")
	jp.LineID = "line-jp"
	jp.Password = "p: # \"quoted\""
	us := target("US")
	us.LineID = "line-us"
	policy := &RoutingPolicy{Fallback: Action{Kind: "line", LineID: "line-jp"}, Rules: []Rule{
		{MatchType: "domain", MatchValue: "example.com", Action: Action{Kind: "line", LineID: "line-us"}},
		{MatchType: "cidr", MatchValue: "2001:db8::/32", Action: Action{Kind: "direct"}},
		{MatchType: "geoip", MatchValue: "cn", Action: Action{Kind: "block"}},
	}}
	data, contentType, err := RenderWithRouting("clash", "{name}", []Target{jp, us}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/yaml" {
		t.Fatalf("content type = %q", contentType)
	}
	var cfg struct {
		Port        int              `yaml:"port"`
		SocksPort   int              `yaml:"socks-port"`
		AllowLAN    bool             `yaml:"allow-lan"`
		Proxies     []map[string]any `yaml:"proxies"`
		ProxyGroups []struct {
			Name, Type string
			Proxies    []string
		} `yaml:"proxy-groups"`
		Rules []string `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7890 || cfg.SocksPort != 7891 || cfg.AllowLAN || len(cfg.Proxies) != 2 {
		t.Fatalf("unsafe or incomplete Clash profile: %+v", cfg)
	}
	if cfg.Proxies[0]["type"] != "trojan" || cfg.Proxies[0]["password"] != jp.Password || cfg.Proxies[0]["sni"] != jp.ServerName {
		t.Fatalf("Clash Trojan = %+v", cfg.Proxies[0])
	}
	if _, ok := cfg.Proxies[0]["network"]; ok {
		t.Fatal("Clash profile includes Mihomo-specific network field")
	}
	for _, want := range []string{"DOMAIN,example.com,LINE:line-us", "IP-CIDR6,2001:db8::/32,DIRECT", "GEOIP,CN,REJECT", "MATCH,LINE:line-jp"} {
		found := false
		for _, rule := range cfg.Rules {
			if rule == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing rule %q in %+v", want, cfg.Rules)
		}
	}
	if strings.Contains(string(data), "mixed-port:") {
		t.Fatal("Clash profile includes Mihomo mixed listener")
	}
}

func TestRenderClashRejectsUnmanagedGeoSite(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{Fallback: Action{Kind: "direct"}, Rules: []Rule{{MatchType: "geosite", MatchValue: "cn", Action: Action{Kind: "direct"}}}}
	if _, _, err := RenderWithRouting("clash", "{name}", []Target{a}, policy); err == nil {
		t.Fatal("Clash accepted unmanaged GeoSite")
	}
}

func TestRenderClashRejectsRuleDelimiterInjection(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{Fallback: Action{Kind: "direct"}, Rules: []Rule{{MatchType: "domain", MatchValue: "example.com,REJECT", Action: Action{Kind: "direct"}}}}
	if _, _, err := RenderWithRouting("clash", "{name}", []Target{a}, policy); err == nil {
		t.Fatal("Clash accepted a rule with an injected delimiter")
	}
}
