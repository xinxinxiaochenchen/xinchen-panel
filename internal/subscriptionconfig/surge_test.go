package subscriptionconfig

import (
	"strings"
	"testing"
)

func TestRenderSurgeTrojanAndRouting(t *testing.T) {
	jp := target("Japan")
	jp.LineID = "line-jp"
	us := target("US")
	us.LineID = "line-us"
	policy := &RoutingPolicy{Fallback: Action{Kind: "line", LineID: "line-jp"}, Rules: []Rule{
		{MatchType: "domain", MatchValue: "example.com", Action: Action{Kind: "line", LineID: "line-us"}},
		{MatchType: "cidr", MatchValue: "2001:db8::/32", Action: Action{Kind: "direct"}},
		{MatchType: "geoip", MatchValue: "cn", Action: Action{Kind: "block"}},
	}}
	data, contentType, err := RenderWithRouting("surge", "{name}", []Target{jp, us}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type: %s", contentType)
	}
	for _, want := range []string{
		"[General]", "http-listen = 127.0.0.1:6152", "[Proxy]",
		"Japan = trojan, edge.example.com, 443, password=secret, sni=tls.example.com, skip-cert-verify=false",
		"[Proxy Group]", "PROXY = select, Japan, US", "LINE:line-jp = select, Japan",
		"[Rule]", "DOMAIN,example.com,LINE:line-us", "IP-CIDR6,2001:db8::/32,DIRECT",
		"GEOIP,CN,REJECT", "FINAL,LINE:line-jp",
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q in %s", want, data)
		}
	}
}

func TestRenderSurgeRejectsDelimiterInjection(t *testing.T) {
	unsafe := target("name, EXTRA")
	if _, _, err := Render("surge", "{name}", []Target{unsafe}); err == nil {
		t.Fatal("accepted comma in proxy name")
	}
	unsafe = target("safe")
	unsafe.Password = "secret\n[Rule]"
	if _, _, err := Render("surge", "{name}", []Target{unsafe}); err == nil {
		t.Fatal("accepted newline in password")
	}
	unsafe = target("Japan")
	unsafe.LineID = "line-jp\n[Rule]"
	policy := &RoutingPolicy{Fallback: Action{Kind: "line", LineID: unsafe.LineID}}
	if _, _, err := RenderWithRouting("surge", "{name}", []Target{unsafe}, policy); err == nil {
		t.Fatal("accepted newline in line ID")
	}
	unsafe = target("#commented")
	if _, _, err := Render("surge", "{name}", []Target{unsafe}); err == nil {
		t.Fatal("accepted comment marker in proxy name")
	}
}

func TestRenderSurgeDefaultRouteAndIPv6Host(t *testing.T) {
	endpoint := target("Japan")
	endpoint.Server = "2001:db8::1"
	data, _, err := Render("surge", "{name}", []Target{endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Japan = trojan, [2001:db8::1], 443") || !strings.Contains(string(data), "FINAL,PROXY") {
		t.Fatalf("invalid default profile: %s", data)
	}
}

func TestRenderSurgeRejectsUnsupportedAndMalformedRules(t *testing.T) {
	endpoint := target("Japan")
	endpoint.LineID = "line-jp"
	for _, rule := range []Rule{
		{MatchType: "geosite", MatchValue: "cn", Action: Action{Kind: "direct"}},
		{MatchType: "domain", MatchValue: "example.com,REJECT", Action: Action{Kind: "direct"}},
		{MatchType: "cidr", MatchValue: "not-a-prefix", Action: Action{Kind: "direct"}},
	} {
		policy := &RoutingPolicy{Fallback: Action{Kind: "line", LineID: "line-jp"}, Rules: []Rule{rule}}
		if _, _, err := RenderWithRouting("surge", "{name}", []Target{endpoint}, policy); err == nil {
			t.Fatalf("accepted invalid rule: %+v", rule)
		}
	}
}
