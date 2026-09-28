package subscriptionconfig

import (
	"strings"
	"testing"
)

func TestRenderSurgeTrojanAndDefaultProxy(t *testing.T) {
	jp := target("Japan")
	jp.LineID = "line-jp"
	us := target("US")
	us.LineID = "line-us"
	data, contentType, err := Render("surge", "{name}", []Target{jp, us})
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type: %s", contentType)
	}
	for _, want := range []string{
		"[General]", "http-listen = 127.0.0.1:6152", "[Proxy]",
		"Japan = trojan, edge.example.com, 443, password=secret, sni=tls.example.com, skip-cert-verify=false",
		"[Proxy Group]", "PROXY = select, Japan, US", "[Rule]", "FINAL,PROXY",
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
