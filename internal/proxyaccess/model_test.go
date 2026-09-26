package proxyaccess

import "testing"

func TestNormalizeAccessValidatesNameAndLine(t *testing.T) {
	id := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	input, err := NormalizeAccess(NewAccess{Name: "  日本入口  ", LineID: id})
	if err != nil || input.Name != "日本入口" || input.LineID != id || !input.Enabled {
		t.Fatalf("normalized input = %+v, %v", input, err)
	}
	for _, item := range []NewAccess{
		{Name: "", LineID: id},
		{Name: "x", LineID: "invalid"},
		{Name: "\n", LineID: id},
		{Name: "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", LineID: id},
	} {
		if _, err := NormalizeAccess(item); err == nil {
			t.Fatalf("accepted invalid access: %+v", item)
		}
	}
}

func TestNormalizeAccessAllowsDisabledCreation(t *testing.T) {
	disabled := false
	input, err := NormalizeAccess(NewAccess{Name: "backup", LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423", Enabled: &disabled})
	if err != nil || input.Enabled {
		t.Fatalf("disabled input = %+v, %v", input, err)
	}
}

func TestNormalizeAccessPatchRequiresAChange(t *testing.T) {
	if _, err := NormalizeAccessPatch(AccessPatch{}); err == nil {
		t.Fatal("empty patch accepted")
	}
	name := "  备用  "
	patch, err := NormalizeAccessPatch(AccessPatch{Name: &name})
	if err != nil || patch.Name == nil || *patch.Name != "备用" {
		t.Fatalf("normalized patch = %+v, %v", patch, err)
	}
}
