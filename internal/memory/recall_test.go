package memory

import (
	"strings"
	"testing"
)

func TestRecallNothingForSmallTalk(t *testing.T) {
	setup(t)
	LogExchange("merci pour le coup de main", "Avec plaisir.")
	if err := UpdateFiche("", "remerciements", "Quand il faut dire merci", "Répondre simplement."); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"merci", "merci beaucoup", "ok", "  "} {
		if got := Recall(t.Context(), text, nil); got != "" {
			t.Errorf("%q : rien à rappeler, reçu %q", text, got)
		}
	}
	if got := Recall(t.Context(), "quelle est la capitale du Pérou", nil); got != "" {
		t.Errorf("rien ne correspond : %q", got)
	}
}

func TestRecallFiche(t *testing.T) {
	setup(t)
	steps := "1. Lancer les tests\n2. Pousser la branche main\n3. Vérifier la page d'accueil"
	if err := UpdateFiche("", "deployer-site", "Quand il faut déployer le site web en production", steps); err != nil {
		t.Fatal(err)
	}
	if err := UpdateFiche("", "recette-crepes", "Quand on veut faire des crêpes", "Farine, œufs, lait. Laisser reposer la pâte avant de déployer la crêpière en production."); err != nil {
		t.Fatal(err)
	}
	got := Recall(t.Context(), "Peux-tu déployer le site en production ce soir ?", nil)
	if !strings.HasPrefix(got, recallHead) || !strings.Contains(got, "Fiche deployer-site (when: Quand il faut déployer le site web en production):\n"+steps) {
		t.Fatalf("la fiche devait être rappelée en entier :\n%s", got)
	}
	if strings.Contains(got, "recette-crepes") {
		t.Errorf("une seule fiche au plus :\n%s", got)
	}
	if uses := ficheUses("deployer-site"); uses != 1 {
		t.Errorf("une fiche rappelée en entier a servi : usage %d", uses)
	}

	// Le but ne couvre qu'un terme, le contenu fait le reste : juste signalée.
	got = Recall(t.Context(), "comment reposer la pâte des crêpes ce matin", nil)
	if !strings.Contains(got, "Maybe relevant: fiche recette-crepes") || strings.Contains(got, "Farine") {
		t.Errorf("la fiche devait être seulement signalée :\n%s", got)
	}

	long := strings.Repeat("Étape détaillée du déploiement. ", 200)
	if err := UpdateFiche("", "deployer-site", "Quand il faut déployer le site web en production", long); err != nil {
		t.Fatal(err)
	}
	got = Recall(t.Context(), "Peux-tu déployer le site en production ce soir ?", nil)
	if !strings.Contains(got, "fiche cut here: fiche_read deployer-site") || len([]rune(got)) > recallFicheChars+400 {
		t.Errorf("fiche longue : coupée et signalée (%d car.)", len([]rune(got)))
	}
}

func TestRecallJournal(t *testing.T) {
	setup(t)
	question := "Quel est le code du portail de la résidence ?"
	LogExchange("Le code du portail de la résidence a changé", "Noté : nouveau code 9080.")
	LogExchange("Le code du portail de la résidence a changé", "Noté : nouveau code 9080.")
	LogExchange(question, "Le code du portail est 4521.")
	LogExchange("Le portail du jardin grince, où est le code couleur de la peinture ?", "Dans la fiche travaux, code RAL 7016.")
	LogExchange("Que mange le chat ce soir ?", "Des croquettes.")

	got := Recall(t.Context(), "Rappelle-moi le code du portail de la résidence", nil)
	if !strings.HasPrefix(got, recallHead+"From your journal") {
		t.Fatalf("souvenirs attendus :\n%s", got)
	}
	lines := strings.Split(strings.TrimPrefix(got, recallHead), "\n")[1:]
	if len(lines) != 2 || !strings.Contains(lines[0], "4521") || !strings.Contains(lines[1], "9080") {
		t.Errorf("deux souvenirs, sans doublon, le plus récent d'abord :\n%s", got)
	}
	if strings.Contains(got, "croquettes") {
		t.Errorf("souvenir sans rapport :\n%s", got)
	}

	// Un échange encore dans le contexte du modèle est déjà sous ses yeux.
	got = Recall(t.Context(), "Rappelle-moi le code du portail de la résidence", []string{"  " + question, "Le code du portail de la résidence a changé"})
	if strings.Contains(got, "4521") || strings.Contains(got, "9080") {
		t.Errorf("échange déjà dans le contexte rappelé :\n%s", got)
	}

	long := strings.Repeat("le code du portail de la résidence ", 30)
	LogExchange(long, "")
	got = Recall(t.Context(), "Rappelle-moi le code du portail de la résidence", nil)
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "- [") && len([]rune(line)) > recallEntryChars+40 {
			t.Errorf("souvenir trop long (%d car.)", len([]rune(line)))
		}
	}
}
