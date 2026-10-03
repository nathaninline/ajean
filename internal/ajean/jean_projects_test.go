package ajean

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestJeanToolsSchemasSansNull : un champ de schéma émis à null (ex. « required:
// null ») est invalide. llama.cpp le tolère, mais les API OpenAI strictes
// (DeepSeek) rejettent avec « null is not of type "array" ». Issue observée sur
// jean_projects, qui n'a aucun paramètre requis.
func TestJeanToolsSchemasSansNull(t *testing.T) {
	nullFields := []string{`"required":null`, `"enum":null`, `"items":null`, `"type":null`}
	for _, tool := range jeanTools() {
		b, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			t.Fatalf("%s : %v", tool.Function.Name, err)
		}
		for _, nf := range nullFields {
			if strings.Contains(string(b), nf) {
				t.Errorf("%s : schéma contient %s : %s", tool.Function.Name, nf, b)
			}
		}
	}

	// jean_projects n'accepte aucun argument : sa clé `required` doit être ABSENTE
	// (le helper l'omet quand vide), pas null ni [].
	proj := jeanProjectTools()[0] // jean_projects
	params, _ := proj.Function.Parameters.(map[string]any)
	if _, present := params["required"]; present {
		t.Errorf("jean_projects : clé required présente (%v), attendue absente", params["required"])
	}

	// Garde-fou inverse : les outils qui exigent un argument gardent leur `required`.
	mem := jeanProjectTools()[1] // jean_project_mem
	mparams, _ := mem.Function.Parameters.(map[string]any)
	req, _ := mparams["required"].([]string)
	if len(req) != 1 || req[0] != "project" {
		t.Errorf("jean_project_mem : required inattendu (%v)", mparams["required"])
	}
}
