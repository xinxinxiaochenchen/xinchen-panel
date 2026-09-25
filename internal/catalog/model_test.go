package catalog

import "testing"

func TestNormalizeGroupCodeAndFields(t *testing.T) {
	group, err := NormalizeGroup(NewGroup{Code: "RFC.JPT1", Name: " Tokyo ", Region: "JP"})
	if err != nil || group.Name != "Tokyo" || !group.Enabled {
		t.Fatalf("normalized group = %+v, %v", group, err)
	}
	for _, code := range []string{"rfc.jpt1", "RFC..JPT1", " RFC.JPT1 ", "RFC_JPT1"} {
		if _, err := NormalizeGroup(NewGroup{Code: code, Name: "Tokyo", Region: "JP"}); err == nil {
			t.Fatalf("accepted invalid group code %q", code)
		}
	}
}

func TestNormalizeNodeEnforcesCapabilitiesAndPorts(t *testing.T) {
	input := NewNode{GroupID: "11111111-1111-7111-8111-111111111111", Name: " Tokyo Exit ",
		Region: "JP", Host: "exit.example.com", Capabilities: []string{"proxy"}, ProxyPort: intPointer(443)}
	node, err := NormalizeNode(input)
	if err != nil || node.Name != "Tokyo Exit" || node.MultiplierMilli != 1000 || !node.Enabled {
		t.Fatalf("normalized node = %+v, %v", node, err)
	}
	invalid := []NewNode{
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"proxy"}},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"forward"}, ProxyPort: intPointer(443)},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: "https://exit.example.com", Capabilities: []string{"forward"}},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"proxy", "proxy"}, ProxyPort: input.ProxyPort},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"unknown"}},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"forward"}, ProxyPort: intPointer(0)},
		{GroupID: input.GroupID, Name: input.Name, Region: input.Region, Host: input.Host, Capabilities: []string{"forward"}, MultiplierMilli: intPointer(0)},
	}
	for i, candidate := range invalid {
		if _, err := NormalizeNode(candidate); err == nil {
			t.Fatalf("accepted invalid node case %d", i)
		}
	}
}

func intPointer(value int) *int { return &value }
