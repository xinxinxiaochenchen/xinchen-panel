package catalog

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const lineNodeID = "11111111-1111-7111-8111-111111111111"
const lineRelayID = "22222222-2222-7222-8222-222222222222"
const lineExitID = "33333333-3333-7333-8333-333333333333"

func TestNormalizeLine(t *testing.T) {
	input := NewLine{Name: "  Japan  ", NodeID: lineNodeID, Tags: []string{" fast ", "japan"}}
	line, err := NormalizeLine(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if line.Name != "Japan" || line.NodeID != lineNodeID || !line.Enabled || line.Priority != 100 || line.Weight != 1 || line.MultiplierMilli != nil || !reflect.DeepEqual(line.Tags, []string{"fast", "japan"}) {
		t.Fatalf("normalized line = %+v", line)
	}
	priority, weight, multiplier, disabled := 0, 100, 500, false
	shared, err := NormalizeLine(NewLine{Name: "Shared", NodeID: lineNodeID, Priority: &priority, Weight: &weight, MultiplierMilli: &multiplier, Enabled: &disabled}, true)
	if err != nil || shared.Priority != 0 || shared.Weight != 100 || shared.MultiplierMilli == nil || *shared.MultiplierMilli != 500 || shared.Enabled {
		t.Fatalf("shared line = %+v, %v", shared, err)
	}
	for _, candidate := range []NewLine{
		{Name: "", NodeID: lineNodeID},
		{Name: strings.Repeat("a", 101), NodeID: lineNodeID},
		{Name: "bad ID", NodeID: "invalid"},
		{Name: "bad priority", NodeID: lineNodeID, Priority: intPtr(-1)},
		{Name: "bad weight", NodeID: lineNodeID, Weight: intPtr(0)},
		{Name: "duplicate tags", NodeID: lineNodeID, Tags: []string{"a", " a "}},
		{Name: "member multiplier", NodeID: lineNodeID, MultiplierMilli: &multiplier},
	} {
		if _, err := NormalizeLine(candidate, false); err == nil {
			t.Fatalf("accepted invalid line: %+v", candidate)
		} else {
			var validation ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("unexpected error type: %v", err)
			}
		}
	}
	if _, err := NormalizeLine(NewLine{Name: "bad multiplier", NodeID: lineNodeID, MultiplierMilli: intPtr(0)}, true); err == nil {
		t.Fatal("accepted zero shared multiplier")
	}
}

func TestNormalizeLineTopology(t *testing.T) {
	for _, hops := range [][]LineHop{
		{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 1, NodeID: lineExitID, Role: "egress"}},
		{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 1, NodeID: lineRelayID, Role: "relay"}, {Position: 2, NodeID: lineExitID, Role: "egress"}},
	} {
		line, err := NormalizeLine(NewLine{Name: "Multi", Hops: hops, Enabled: boolPtr(false)}, false)
		if err != nil || line.NodeID != "" || !reflect.DeepEqual(line.Hops, hops) || line.Enabled {
			t.Fatalf("normalized multi-hop = %+v, %v", line, err)
		}
	}
	for _, input := range []NewLine{
		{Name: "missing"},
		{Name: "both", NodeID: lineNodeID, Hops: []LineHop{{Position: 0, NodeID: lineNodeID, Role: "egress"}}},
		{Name: "single alternate", Hops: []LineHop{{Position: 0, NodeID: lineNodeID, Role: "egress"}}, Enabled: boolPtr(false)},
		{Name: "reordered", Hops: []LineHop{{Position: 1, NodeID: lineExitID, Role: "egress"}, {Position: 0, NodeID: lineNodeID, Role: "ingress"}}, Enabled: boolPtr(false)},
		{Name: "gap", Hops: []LineHop{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 2, NodeID: lineExitID, Role: "egress"}}, Enabled: boolPtr(false)},
		{Name: "duplicate", Hops: []LineHop{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 1, NodeID: lineNodeID, Role: "egress"}}, Enabled: boolPtr(false)},
		{Name: "middle role", Hops: []LineHop{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 1, NodeID: lineRelayID, Role: "egress"}, {Position: 2, NodeID: lineExitID, Role: "egress"}}, Enabled: boolPtr(false)},
		{Name: "invalid id", Hops: []LineHop{{Position: 0, NodeID: "bad", Role: "ingress"}, {Position: 1, NodeID: lineExitID, Role: "egress"}}, Enabled: boolPtr(false)},
	} {
		if _, err := NormalizeLine(input, false); err == nil {
			t.Fatalf("accepted invalid topology: %+v", input)
		}
	}
}

func TestNormalizeEnabledMultiHopTopology(t *testing.T) {
	hops := []LineHop{{Position: 0, NodeID: lineNodeID, Role: "ingress"}, {Position: 1, NodeID: lineExitID, Role: "egress"}}
	line, err := NormalizeLine(NewLine{Name: "Enabled route", Hops: hops, Enabled: boolPtr(true)}, false)
	if err != nil || !line.Enabled || !reflect.DeepEqual(line.Hops, hops) {
		t.Fatalf("enabled route = %+v, %v", line, err)
	}
}

func intPtr(value int) *int { return &value }

func TestNormalizeLinePatch(t *testing.T) {
	name := "  Renamed  "
	tags := []string{" fast "}
	patch, err := NormalizeLinePatch(LinePatch{Name: &name, Enabled: boolPtr(false), Tags: &tags}, false)
	if err != nil || patch.Name == nil || *patch.Name != "Renamed" || patch.Enabled == nil || *patch.Enabled || patch.Tags == nil || !reflect.DeepEqual(*patch.Tags, []string{"fast"}) {
		t.Fatalf("normalized patch = %+v, %v", patch, err)
	}
	for _, candidate := range []LinePatch{
		{},
		{Name: stringPtr(" ")},
		{Priority: intPtr(-1)},
		{Weight: intPtr(0)},
		{Tags: &[]string{"a", "a"}},
		{MultiplierMilli: intPtr(500)},
	} {
		if _, err := NormalizeLinePatch(candidate, false); err == nil {
			t.Fatalf("accepted invalid member patch: %+v", candidate)
		}
	}
}

func boolPtr(value bool) *bool       { return &value }
func stringPtr(value string) *string { return &value }
