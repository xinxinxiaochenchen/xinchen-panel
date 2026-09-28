package subscriptionconfig

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderClashTrojanAndDefaultProxy(t *testing.T) {
	jp := target("Japan")
	jp.LineID = "line-jp"
	jp.Password = "p: # \"quoted\""
	us := target("US")
	us.LineID = "line-us"
	data, contentType, err := Render("clash", "{name}", []Target{jp, us})
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
	if len(cfg.ProxyGroups) != 1 || cfg.ProxyGroups[0].Name != "PROXY" || len(cfg.ProxyGroups[0].Proxies) != 2 || len(cfg.Rules) != 1 || cfg.Rules[0] != "MATCH,PROXY" {
		t.Fatalf("invalid default proxy selection: %+v", cfg)
	}
	if strings.Contains(string(data), "mixed-port:") {
		t.Fatal("Clash profile includes Mihomo mixed listener")
	}
}
