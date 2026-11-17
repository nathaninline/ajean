package agent

import (
	"strings"
	"testing"
)

func TestSnippetAndLex(t *testing.T) {
	s := snippet("| Colonne | Valeur |\n|---|---|\n| **Vitesse** | 40 tok/s |", []string{"vitesse"})
	if strings.ContainsAny(s, "|*") || !strings.Contains(s, "Vitesse") {
		t.Fatalf("extrait : %q", s)
	}
	if got := lexScore(strings.Fields(foldText("Élève rapide")), foldText("un eleve tres RAPIDE")); got != 1 {
		t.Fatalf("lexScore = %v", got)
	}
	if v := cosVec(quantize(unitVec([]float32{1, 2, 3})), unitVec([]float32{1, 2, 3})); v < 0.99 {
		t.Fatalf("cos = %v", v)
	}
}
