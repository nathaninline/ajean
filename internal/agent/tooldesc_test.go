package agent

import (
	"testing"

	"ajean/internal/llm"
)

func TestToolDesc(t *testing.T) {
	specs := []llm.Tool{
		{Type: "function", Function: llm.Function{Name: "a", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}, "required": []string{"q"}}}},
		{Type: "function", Function: llm.Function{Name: "b", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"description": map[string]any{"type": "string"}}}}},
	}
	out, set := withToolDesc(specs)
	req := out[0].Function.Parameters["required"].([]string)
	if !set["a"] || set["b"] || len(req) != 2 || req[0] != toolDescParam {
		t.Fatalf("specs = %+v, set = %v", out, set)
	}
	if _, ok := specs[0].Function.Parameters["properties"].(map[string]any)[toolDescParam]; ok {
		t.Error("les specs d'origine ne doivent pas changer")
	}

	c := call{name: "a", args: map[string]any{"q": "x", "description": "  Recherche   de x. "}}
	if d := takeToolDesc(set, &c); d != "Recherche de x." {
		t.Errorf("desc = %q", d)
	}
	if _, left := c.args["description"]; left {
		t.Error("la description doit être retirée des arguments")
	}
	own := call{name: "b", args: map[string]any{"description": "à garder"}}
	if d := takeToolDesc(set, &own); d != "" || own.args["description"] != "à garder" {
		t.Error("un outil avec son propre champ description garde ses arguments")
	}
}
