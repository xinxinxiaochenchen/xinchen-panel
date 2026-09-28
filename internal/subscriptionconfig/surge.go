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

func renderSurge(targets []Target, names []string) ([]byte, error) {
	for i, target := range targets {
		if err := surgeName(names[i]); err != nil {
			return nil, fmt.Errorf("target %d: %w", i+1, err)
		}
		if err := surgePassword(target.Password); err != nil {
			return nil, fmt.Errorf("target %d: %w", i+1, err)
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
	b.WriteString("\n[Rule]\nFINAL,PROXY\n")
	return b.Bytes(), nil
}
