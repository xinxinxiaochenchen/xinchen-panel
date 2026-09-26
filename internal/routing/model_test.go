package routing

import "testing"

func TestNormalizeProfileAndRule(t *testing.T) {
	profile, err := NormalizeProfile(NewProfile{Name: "  Travel  ", FallbackKind: "direct", Enabled: nil})
	if err != nil || profile.Name != "Travel" || profile.FallbackKind != "direct" || !profile.Enabled {
		t.Fatalf("profile = %+v, %v", profile, err)
	}
	lineID := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	rule, err := NormalizeRule(NewRule{Priority: 10, MatchType: "domain_suffix", MatchValue: " .example.com. ", Action: "line", LineID: &lineID})
	if err != nil || rule.MatchValue != "example.com" || rule.LineID == nil || *rule.LineID != lineID {
		t.Fatalf("rule = %+v, %v", rule, err)
	}
}

func TestNormalizeRuleRejectsUnsafeOrInvalidTargets(t *testing.T) {
	lineID := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	for _, input := range []NewRule{
		{Priority: 0, MatchType: "domain", MatchValue: "example.com", Action: "direct"},
		{Priority: 1, MatchType: "domain", MatchValue: "example.com", Action: "line"},
		{Priority: 1, MatchType: "domain", MatchValue: "*.example.com", Action: "direct"},
		{Priority: 1, MatchType: "cidr", MatchValue: "192.0.2.0/24", Action: "direct", LineID: &lineID},
		{Priority: 1, MatchType: "geoip", MatchValue: "C N", Action: "direct"},
		{Priority: 1, MatchType: "geosite", MatchValue: "cn", Action: "direct", LineID: &lineID},
		{Priority: 1, MatchType: "domain", MatchValue: "example.com", Action: "bogus"},
	} {
		if _, err := NormalizeRule(input); err == nil {
			t.Fatalf("accepted invalid rule %+v", input)
		}
	}
	if _, err := NormalizeProfile(NewProfile{Name: "x", FallbackKind: "line"}); err == nil {
		t.Fatal("accepted line fallback without target")
	}
}

func TestNormalizeRuleCanonicalizesIPAndCIDR(t *testing.T) {
	for input, want := range map[string]string{" 2001:DB8::1 ": "2001:db8::1", "10.0.0.0/8": "10.0.0.0/8"} {
		kind := "ip"
		if want == "10.0.0.0/8" {
			kind = "cidr"
		}
		rule, err := NormalizeRule(NewRule{Priority: 1, MatchType: kind, MatchValue: input, Action: "block"})
		if err != nil || rule.MatchValue != want {
			t.Fatalf("%s => %+v, %v", input, rule, err)
		}
	}
}
