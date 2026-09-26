package subscriptionconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderMihomoRoutingUsesExportedLineSelector(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	b := target("US")
	b.LineID = "line-us"
	policy := &RoutingPolicy{
		Fallback: Action{Kind: "line", LineID: "line-jp"},
		Rules: []Rule{
			{MatchType: "domain", MatchValue: "example.com", Action: Action{Kind: "line", LineID: "line-us"}},
			{MatchType: "cidr", MatchValue: "192.0.2.0/24", Action: Action{Kind: "direct"}},
		},
	}
	data, _, err := RenderWithRouting("mihomo", "{name}", []Target{a, b}, policy)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"LINE:line-jp", "LINE:line-us", "DOMAIN,example.com,LINE:line-us", "IP-CIDR,192.0.2.0/24,DIRECT", "MATCH,LINE:line-jp"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
}

func TestRenderSingBoxRoutingAndUnsupportedGeo(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{Fallback: Action{Kind: "direct"}, Rules: []Rule{{MatchType: "domain_suffix", MatchValue: "example.com", Action: Action{Kind: "line", LineID: "line-jp"}}}}
	data, _, err := RenderWithRouting("sing-box", "{name}", []Target{a}, policy)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Route struct {
			Final string
			Rules []map[string]any
		}
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Route.Final != "DIRECT" || len(cfg.Route.Rules) != 1 || cfg.Route.Rules[0]["outbound"] != "LINE:line-jp" || cfg.Route.Rules[0]["domain_suffix"] == nil {
		t.Fatalf("unexpected route: %+v", cfg.Route)
	}
	policy.Rules[0].MatchType = "geoip"
	policy.Rules[0].MatchValue = "cn"
	if _, _, err := RenderWithRouting("sing-box", "{name}", []Target{a}, policy); err == nil {
		t.Fatal("accepted geoip without a rule-set")
	}
	policy.Rules[0].MatchType = "geosite"
	if _, _, err := RenderWithRouting("mihomo", "{name}", []Target{a}, policy); err == nil {
		t.Fatal("accepted geosite without a managed data source")
	}
}

func TestRenderManagedGeoRulesExpandToPortableClientRules(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{
		Fallback: Action{Kind: "direct"},
		RuleSets: map[string]RuleSet{
			"geosite:ai": {Kind: "geosite", Code: "ai", Entries: []string{"domain:openai.com", "suffix:anthropic.com"}},
			"geoip:cn":   {Kind: "geoip", Code: "cn", Entries: []string{"1.0.1.0/24", "240e::/16"}},
		},
		Rules: []Rule{
			{MatchType: "geosite", MatchValue: "ai", Action: Action{Kind: "line", LineID: a.LineID}},
			{MatchType: "geoip", MatchValue: "cn", Action: Action{Kind: "direct"}},
		},
	}
	for _, format := range []string{"clash", "mihomo", "sing-box", "surge"} {
		data, _, err := RenderWithRouting(format, "{name}", []Target{a}, policy)
		if err != nil {
			t.Fatalf("%s rejected managed geo rules: %v", format, err)
		}
		text := string(data)
		for _, want := range []string{"openai.com", "anthropic.com", "1.0.1.0/24", "240e::/16"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s missing expanded entry %q in %s", format, want, text)
			}
		}
		if format == "mihomo" && !strings.Contains(text, "IP-CIDR6,240e::/16,DIRECT") {
			t.Fatalf("mihomo must use IPv6 CIDR rule type: %s", text)
		}
	}
}

func TestRenderManagedGeoRulesRejectsInvalidEntries(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	for _, entry := range []string{"example.com,REJECT", "domain:bad domain", "suffix:evil.com\nMATCH,REJECT"} {
		policy := &RoutingPolicy{Fallback: Action{Kind: "direct"}, Rules: []Rule{{MatchType: "geosite", MatchValue: "ai", Action: Action{Kind: "direct"}}}, RuleSets: map[string]RuleSet{"geosite:ai": {Kind: "geosite", Code: "ai", Entries: []string{entry}}}}
		if _, _, err := RenderWithRouting("mihomo", "{name}", []Target{a}, policy); err == nil {
			t.Fatalf("accepted invalid managed entry %q", entry)
		}
	}
}

func TestRenderRoutingRejectsMissingExportedLine(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{Fallback: Action{Kind: "line", LineID: "line-us"}}
	if _, _, err := RenderWithRouting("mihomo", "", []Target{a}, policy); err == nil {
		t.Fatal("silently routed to a different line")
	}
}

func TestRenderRoutingBlockAndExactIPv6(t *testing.T) {
	a := target("Japan")
	a.LineID = "line-jp"
	policy := &RoutingPolicy{Fallback: Action{Kind: "block"}, Rules: []Rule{{MatchType: "ip", MatchValue: "2001:db8::1", Action: Action{Kind: "direct"}}}}
	mihomo, _, err := RenderWithRouting("mihomo", "{name}", []Target{a}, policy)
	if err != nil || !strings.Contains(string(mihomo), "IP-CIDR6,2001:db8::1/128,DIRECT") || !strings.Contains(string(mihomo), "MATCH,REJECT") {
		t.Fatalf("mihomo block/IP rule: %v %s", err, mihomo)
	}
	sing, _, err := RenderWithRouting("sing-box", "{name}", []Target{a}, policy)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Route struct{ Rules []map[string]any }
	}
	if err := json.Unmarshal(sing, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Route.Rules) != 2 || cfg.Route.Rules[0]["ip_cidr"] == nil || cfg.Route.Rules[1]["action"] != "reject" {
		t.Fatalf("sing-box block/IP rule: %+v", cfg.Route.Rules)
	}
}
