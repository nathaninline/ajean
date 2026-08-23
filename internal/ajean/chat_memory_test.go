package ajean

import (
	"strings"
	"testing"
)

// Reproduit le bug rapporté : une petite page dont le NOM et le TITRE contiennent
// « copine » ne remontait pas pour la requête « copine Alice », noyée par de
// grosses pages qui répètent « Alice » des dizaines de fois. Le classement doit
// mettre en tête la page qui matche le PLUS de termes distincts (couverture),
// puis pondérer par la rareté.
func TestMemSearchCoverageBeatsFrequency(t *testing.T) {
	testHome(t)

	// Deux pages « bruyantes » bourrées de « Alice », sans « copine ».
	if err := MemAdd("business-alice.md", "# Business Alice\n"+strings.Repeat("Alice veut un business. ", 40)); err != nil {
		t.Fatal(err)
	}
	if err := MemAdd("guide-ajean.md", "# Guide AJEAN\n"+strings.Repeat("projet de Alice. ", 30)); err != nil {
		t.Fatal(err)
	}
	// La petite page cible : matche les DEUX termes de la requête.
	if err := MemAdd("profil-copine-alice.md", "# Profil : Copine de Alice\n- Prénom : Emma\n"); err != nil {
		t.Fatal(err)
	}

	hits := MemSearch("copine Alice", 8)
	if len(hits) == 0 {
		t.Fatal("aucun résultat pour « copine Alice »")
	}
	if hits[0].File != "profil-copine-alice.md" {
		var names []string
		for _, h := range hits {
			names = append(names, h.File)
		}
		t.Fatalf("la page copine devrait être 1re (matche les 2 termes), obtenu ordre : %v", names)
	}
}

// Une requête mono-terme rare doit privilégier un match dans le nom/titre.
func TestMemSearchNameTitleBoost(t *testing.T) {
	testHome(t)
	_ = MemAdd("notes-diverses.md", "# Notes\nUn jour Emma est passée dire bonjour, puis repartie.\n")
	_ = MemAdd("profil-copine.md", "# Profil : Copine\n- Prénom : Emma\n")
	hits := MemSearch("copine", 8)
	if len(hits) == 0 || hits[0].File != "profil-copine.md" {
		t.Fatalf("la page dont le nom/titre porte « copine » devrait être 1re, obtenu : %+v", hits)
	}
}
