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

func TestNormalizeAccessLineCandidatesPreserveOrderAndRejectDuplicates(t *testing.T) {
	first := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	second := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424"
	input, err := NormalizeAccess(NewAccess{Name: "pool", LineIDs: []string{first, second}})
	if err != nil || input.LineID != first || len(input.LineIDs) != 2 || input.LineIDs[1] != second {
		t.Fatalf("normalized candidates = %+v, %v", input, err)
	}
	for _, invalid := range []NewAccess{
		{Name: "pool", LineIDs: []string{}},
		{Name: "pool", LineIDs: []string{first, first}},
		{Name: "pool", LineID: second, LineIDs: []string{first, second}},
		{Name: "pool", LineIDs: []string{first, "bad"}},
	} {
		if _, err := NormalizeAccess(invalid); err == nil {
			t.Fatalf("invalid candidates accepted: %+v", invalid)
		}
	}
}

func TestNormalizeAccessLineOptionsRequireMatchingLinesAndBoundedWeights(t *testing.T) {
	first := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e423"
	second := "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e424"
	got, err := NormalizeAccess(NewAccess{Name: "pool", LineIDs: []string{first, second}, LineOptions: []LineOption{
		{LineID: second, Priority: 20, Weight: 3}, {LineID: first, Priority: 10, Weight: 1},
	}})
	if err != nil || len(got.LineOptions) != 2 || got.LineOptions[0].LineID != first || got.LineOptions[0].Weight != 1 || got.LineOptions[1].LineID != second || got.LineOptions[1].Weight != 3 {
		t.Fatalf("line options = %+v, %v", got, err)
	}
	for _, options := range [][]LineOption{
		{{LineID: first, Priority: 10, Weight: 1}},
		{{LineID: first, Priority: 10, Weight: 1}, {LineID: first, Priority: 20, Weight: 1}},
		{{LineID: first, Priority: 10, Weight: 0}, {LineID: second, Priority: 20, Weight: 1}},
		{{LineID: first, Priority: 10, Weight: 1}, {LineID: "018f7d37-c20e-7a6a-8bb8-b0c3a4d3e425", Priority: 20, Weight: 1}},
	} {
		if _, err := NormalizeAccess(NewAccess{Name: "pool", LineIDs: []string{first, second}, LineOptions: options}); err == nil {
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
}
