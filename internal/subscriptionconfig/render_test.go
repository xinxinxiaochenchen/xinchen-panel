package subscriptionconfig

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func target(name string) Target {
	return Target{
		ID: name, Name: name, LineName: "线路", Region: "香港",
		Server: "edge.example.com", ServerName: "tls.example.com",
		Port: 443, Password: "secret",
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, template := range []string{"", "{name}", "{line}/{region} #{index}"} {
		if err := ValidateTemplate(template); err != nil {
			t.Errorf("valid %q: %v", template, err)
		}
	}
	for _, template := range []string{"{unknown}", "{name", "name}", "{name}\n", "{name}\x7f", strings.Repeat("a", 201)} {
		if err := ValidateTemplate(template); err == nil {
			t.Errorf("accepted invalid template %q", template)
		}
	}
}

func TestRenderSingBox(t *testing.T) {
	a := target("test \"\\")
	a.Server = "edge.example.com"
	a.Password = "p:\"\\\n"
	b := target("test \"\\")
	b.ID = "other"
	b.Server = "192.0.2.5"
	b.Port = 8443
	b.Password = "second"
	data, contentType, err := Render("sing-box", "{region} · {name} {line} {index}", []Target{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/json" {
		t.Fatalf("content type: %s", contentType)
	}
	var cfg struct {
		DNS struct {
			Servers []struct{ Type, Tag string }
			Final   string
		}
		Inbounds []struct {
			Type, Tag, Listen string
			ListenPort        int `json:"listen_port"`
		}
		Outbounds []struct {
			Type, Tag, Server, Password string
			ServerPort                  int `json:"server_port"`
			Outbounds                   []string
			TLS                         struct {
				Enabled, Insecure bool
				ServerName        string `json:"server_name"`
			}
		}
		Route struct{ Final string }
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inbounds) != 1 || cfg.Inbounds[0].Type != "mixed" || cfg.Inbounds[0].Listen != "127.0.0.1" || cfg.Inbounds[0].ListenPort != 7890 {
		t.Fatalf("unsafe inbound: %+v", cfg.Inbounds)
	}
	if len(cfg.Outbounds) != 3 || cfg.Outbounds[0].Type != "selector" || cfg.Route.Final != cfg.Outbounds[0].Tag {
		t.Fatalf("unsafe routing: %+v", cfg)
	}
	if len(cfg.Outbounds[0].Outbounds) != 2 || cfg.Outbounds[0].Outbounds[0] != cfg.Outbounds[1].Tag || cfg.Outbounds[0].Outbounds[1] != cfg.Outbounds[2].Tag {
		t.Fatalf("selector mismatch: %+v", cfg.Outbounds)
	}
	for i, want := range []Target{a, b} {
		got := cfg.Outbounds[i+1]
		if got.Type != "trojan" || got.Server != want.Server || got.ServerPort != want.Port || got.Password != want.Password || !got.TLS.Enabled || got.TLS.Insecure || got.TLS.ServerName != want.ServerName {
			t.Fatalf("outbound %d: %+v", i, got)
		}
	}
	if cfg.Outbounds[1].Tag == cfg.Outbounds[2].Tag {
		t.Fatal("duplicate tags")
	}
	if len(cfg.DNS.Servers) != 1 || cfg.DNS.Servers[0].Type != "local" || cfg.DNS.Final != cfg.DNS.Servers[0].Tag {
		t.Fatalf("invalid DNS: %+v", cfg.DNS)
	}
	if strings.Contains(string(data), "\"direct\"") {
		t.Fatal("unexpected direct fallback")
	}
}

func TestRenderMihomo(t *testing.T) {
	a := target("a: \"quoted\"")
	a.Password = "p: # \"quoted\"\nnext"
	data, contentType, err := Render("mihomo", "", []Target{a, target("same"), target("same")})
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "application/yaml" {
		t.Fatalf("content type: %s", contentType)
	}
	s := string(data)
	for _, part := range []string{"mixed-port: 7890", "allow-lan: false", "bind-address: \"127.0.0.1\"", "type: trojan", "network: tcp", "skip-cert-verify: false", "proxy-groups:", "MATCH,PROXY"} {
		if !strings.Contains(s, part) {
			t.Errorf("missing %q in %s", part, s)
		}
	}
	if strings.Contains(s, "DIRECT") {
		t.Fatal("direct fallback")
	}
	if strings.Contains(s, "quoted\"\nnext") {
		t.Fatal("password escaped as raw newline")
	}
	if !strings.Contains(s, "p: # \\\"quoted\\\"\\nnext") {
		t.Fatal("password not safely quoted")
	}
	if strings.Count(s, "type: trojan") != 3 {
		t.Fatalf("wrong proxy count: %s", s)
	}
	if !strings.Contains(s, "same (2)") {
		t.Fatal("missing duplicate disambiguation")
	}
}

func TestRenderRejectsInvalid(t *testing.T) {
	for _, tt := range []struct {
		format  string
		targets []Target
	}{
		{"other", []Target{target("a")}},
		{"mihomo", nil},
		{"sing-box", nil},
		{"mihomo", []Target{func() Target { x := target("a"); x.Password = ""; return x }()}},
		{"sing-box", []Target{func() Target { x := target("a"); x.Server = "bad/server"; return x }()}},
		{"sing-box", []Target{func() Target { x := target("a"); x.ServerName = ""; return x }()}},
		{"mihomo", []Target{func() Target { x := target("a"); x.Port = 0; return x }()}},
		{"mihomo", []Target{func() Target { x := target("a"); x.Port = 65536; return x }()}},
		{"mihomo", []Target{func() Target { x := target("a"); x.Name = "bad\nname"; return x }()}},
	} {
		if _, _, err := Render(tt.format, "", tt.targets); err == nil {
			t.Errorf("accepted format=%q targets=%+v", tt.format, tt.targets)
		}
	}
}

func TestReservedNamesAndIndex(t *testing.T) {
	data, _, err := Render("sing-box", "{name}", []Target{target("PROXY"), target("DIRECT"), target("PROXY"), target("PROXY (2)")})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct{ Outbounds []struct{ Tag string } }
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, outbound := range cfg.Outbounds {
		if seen[outbound.Tag] {
			t.Fatalf("duplicate tag %q", outbound.Tag)
		}
		seen[outbound.Tag] = true
	}
	if cfg.Outbounds[0].Tag != "PROXY" {
		t.Fatalf("selector: %q", cfg.Outbounds[0].Tag)
	}
	data, _, err = Render("sing-box", "{index}", []Target{target("a"), target("b")})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Outbounds[1].Tag != "1" || cfg.Outbounds[2].Tag != "2" {
		t.Fatalf("indices: %+v", cfg.Outbounds)
	}
}

func TestMihomoYAMLRoundTrip(t *testing.T) {
	a := target("name: # \"quoted\"")
	a.Password = "p: # \"quoted\"\nnext"
	a.Server = "192.0.2.2"
	data, _, err := Render("mihomo", "{name}", []Target{a})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MixedPort   int    `yaml:"mixed-port"`
		AllowLAN    bool   `yaml:"allow-lan"`
		BindAddress string `yaml:"bind-address"`
		Proxies     []struct {
			Name, Type, Server, Password, SNI, Network string
			Port                                       int
			SkipCertVerify                             bool `yaml:"skip-cert-verify"`
		} `yaml:"proxies"`
		ProxyGroups []struct {
			Name, Type string
			Proxies    []string
		} `yaml:"proxy-groups"`
		Rules []string
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MixedPort != 7890 || cfg.AllowLAN || cfg.BindAddress != "127.0.0.1" {
		t.Fatalf("unsafe listener: %+v", cfg)
	}
	if len(cfg.Proxies) != 1 || cfg.Proxies[0].Name != a.Name || cfg.Proxies[0].Type != "trojan" ||
		cfg.Proxies[0].Server != a.Server || cfg.Proxies[0].Port != a.Port ||
		cfg.Proxies[0].Password != a.Password || cfg.Proxies[0].SNI != a.ServerName ||
		cfg.Proxies[0].Network != "tcp" || cfg.Proxies[0].SkipCertVerify {
		t.Fatalf("incorrect proxy: %+v", cfg.Proxies)
	}
	if len(cfg.ProxyGroups) != 1 || cfg.ProxyGroups[0].Name != "PROXY" ||
		len(cfg.ProxyGroups[0].Proxies) != 1 || cfg.ProxyGroups[0].Proxies[0] != a.Name ||
		len(cfg.Rules) != 1 || cfg.Rules[0] != "MATCH,PROXY" {
		t.Fatalf("incorrect routing: %+v", cfg)
	}
}

func TestSingBoxDomainResolverAndTCP(t *testing.T) {
	data, _, err := Render("sing-box", "", []Target{target("a")})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Route struct {
			DefaultDomainResolver string `json:"default_domain_resolver"`
		}
		Outbounds []struct{ Tag, Network string }
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Route.DefaultDomainResolver != "local-dns" {
		t.Fatalf("missing outbound DNS resolver: %s", data)
	}
	if cfg.Outbounds[1].Network != "tcp" {
		t.Fatal("Trojan must be TCP only")
	}
	if cfg.Outbounds[1].Tag != "香港 · a" {
		t.Fatalf("default name: %q", cfg.Outbounds[1].Tag)
	}
}

func TestRenderNamesAreDeterministicAndPlaceholdersAreLiteral(t *testing.T) {
	targets := []Target{target("{index}"), target("{index}")}
	first, _, err := Render("sing-box", "{name}/{line}/{region}/{index}", targets)
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := Render("sing-box", "{name}/{line}/{region}/{index}", targets)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(again) {
		t.Fatal("nondeterministic output")
	}
	var cfg struct{ Outbounds []struct{ Tag string } }
	if err := json.Unmarshal(first, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Outbounds[1].Tag != "{index}/线路/香港/1" || cfg.Outbounds[2].Tag != "{index}/线路/香港/2" {
		t.Fatalf("placeholder expansion: %+v", cfg.Outbounds)
	}
}

func TestRenderRejectsOverlongValuesAndInvalidUTF8(t *testing.T) {
	for _, mutate := range []func(*Target){
		func(x *Target) { x.Name = strings.Repeat("a", 101) },
		func(x *Target) { x.LineName = strings.Repeat("a", 101) },
		func(x *Target) { x.Region = strings.Repeat("a", 101) },
		func(x *Target) { x.Password = strings.Repeat("a", 4097) },
		func(x *Target) { x.Password = string([]byte{0xff}) },
		func(x *Target) { x.Name = string([]byte{0xff}) },
	} {
		x := target("a")
		mutate(&x)
		if _, _, err := Render("sing-box", "", []Target{x}); err == nil {
			t.Errorf("accepted invalid target: %+v", x)
		}
	}
	if err := ValidateTemplate(string([]byte{0xff})); err == nil {
		t.Fatal("accepted invalid UTF-8 template")
	}
}

func TestMihomoExplicitlyDisablesUDP(t *testing.T) {
	data, _, err := Render("mihomo", "", []Target{target("a")})
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Proxies []struct {
			UDP *bool `yaml:"udp"`
		}
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Proxies[0].UDP == nil || *cfg.Proxies[0].UDP {
		t.Fatal("UDP must be explicitly disabled")
	}
}

func TestRenderAllowsHundredRuneDisplayValues(t *testing.T) {
	value := strings.Repeat("汉", 100)
	x := target(value)
	x.ID = "id"
	x.LineName = value
	x.Region = value
	if _, _, err := Render("sing-box", "{name}", []Target{x}); err != nil {
		t.Fatalf("100-rune display values should be accepted: %v", err)
	}
}

func TestRenderRejectsExcessivelyExpandedName(t *testing.T) {
	x := target(strings.Repeat("汉", 100))
	x.ID = "id"
	if _, _, err := Render("sing-box", strings.Repeat("{name}", 30), []Target{x}); err == nil {
		t.Fatal("accepted excessively expanded display name")
	}
}

func TestRejectBlankTemplateBeforePersistence(t *testing.T) {
	if err := ValidateTemplate("   "); err == nil {
		t.Fatal("blank template accepted")
	}
}

func TestTemplateLengthUsesUnicodeCharacters(t *testing.T) {
	if err := ValidateTemplate(strings.Repeat("日", 100)); err != nil {
		t.Fatalf("valid 100-character template: %v", err)
	}
	if err := ValidateTemplate(strings.Repeat("日", 201)); err == nil {
		t.Fatal("accepted 201-character template")
	}
}

func TestRejectTemplateThatCanOverflowRenderedName(t *testing.T) {
	if err := ValidateTemplate(strings.Repeat("{name}", 20)); err == nil {
		t.Fatal("template could expand beyond supported name limit")
	}
}
