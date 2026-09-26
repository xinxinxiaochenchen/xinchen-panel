package georules

import (
	"strings"
	"testing"
)

func TestNormalizeGeoSiteAndGeoIP(t *testing.T) {
	for _, tc := range []struct {
		in   NewRuleSet
		want []string
	}{
		{NewRuleSet{Kind: "geosite", Code: "AI", Name: "AI services", Version: "2026.09", Source: "operator upload", Entries: []string{"Domain:OpenAI.com.", "suffix:Anthropic.com"}}, []string{"domain:openai.com", "suffix:anthropic.com"}},
		{NewRuleSet{Kind: "geoip", Code: "CN", Name: "China IP", Version: "2026.09", Source: "operator upload", Entries: []string{"1.0.1.42/24", "240e:1234::/16"}}, []string{"1.0.1.0/24", "240e::/16"}},
	} {
		got, err := Normalize(tc.in)
		if err != nil {
			t.Fatal(err)
		}
		if got.Code != strings.ToLower(tc.in.Code) || strings.Join(got.Entries, ",") != strings.Join(tc.want, ",") || len(got.SHA256) != 64 {
			t.Fatalf("normalized = %+v", got)
		}
	}
}

func TestNormalizeRejectsUnboundedAndUnsafeEntries(t *testing.T) {
	base := NewRuleSet{Kind: "geosite", Code: "ai", Name: "AI", Version: "v1", Source: "operator", Entries: []string{"suffix:example.com"}}
	for _, entries := range [][]string{{}, {"suffix:example.com", "example.com"}, {"example.com,REJECT"}, {"suffix:evil.com\nMATCH,REJECT"}} {
		in := base
		in.Entries = entries
		if _, err := Normalize(in); err == nil {
			t.Fatalf("accepted entries %+v", entries)
		}
	}
	in := base
	in.Entries = []string{"10.0.0.0/8"}
	if _, err := Normalize(in); err == nil {
		t.Fatal("accepted IP prefix in geosite set")
	}
	in = base
	in.Kind = "geoip"
	if _, err := Normalize(in); err == nil {
		t.Fatal("accepted domain in geoip set")
	}
}
