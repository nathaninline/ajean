package ajean

import (
	"strings"
	"testing"
)

func TestParseWhenGranularities(t *testing.T) {
	for _, w := range []string{"2026", "2026-07", "2026-07-15", "2026-07-15 14:30", "2026-07-15T14:30"} {
		if _, _, err := parseWhen(w); err != nil {
			t.Fatalf("parseWhen(%q) a échoué : %v", w, err)
		}
	}
	// Sans heure → dateOnly ; avec heure → pas dateOnly.
	if _, dOnly, _ := parseWhen("2026-07-15"); !dOnly {
		t.Fatal("une date sans heure devrait être dateOnly")
	}
	if _, dOnly, _ := parseWhen("2026-07-15 14:30"); dOnly {
		t.Fatal("une date AVEC heure ne devrait pas être dateOnly")
	}
	if _, _, err := parseWhen("pas une date"); err == nil {
		t.Fatal("une date invalide aurait dû être refusée")
	}
	if ts, _, err := parseWhen(""); err != nil || ts == 0 {
		t.Fatalf("when vide devrait valoir maintenant : %v %d", err, ts)
	}
}

func TestWhenParts(t *testing.T) {
	cases := map[string]int{"": 0, "2026": 1, "2026-07": 2, "2026-07-15": 3, "2026-07-15 14:30": 4}
	for w, want := range cases {
		if got := whenParts(w); got != want {
			t.Fatalf("whenParts(%q) = %d, veut %d", w, got, want)
		}
	}
}

func TestTrackerAddListView(t *testing.T) {
	testHome(t)
	// Le projet actif est un cache global : on le fixe pour être indépendant de
	// l'ordre des tests (un test précédent a pu basculer sur un autre projet).
	_ = setActiveProject(defaultProjectSlug)

	if _, err := trackerAdd("Abonnés", "2025-03-10", "1000"); err != nil {
		t.Fatal(err)
	}
	if _, err := trackerAdd("Abonnés", "2026-07-05", "4000"); err != nil {
		t.Fatal(err)
	}
	id, err := trackerAdd("Abonnés", "2026-07-20", "4210")
	if err != nil {
		t.Fatal(err)
	}

	// Liste : un tracker, 3 points.
	list := trackerList()
	if len(list) != 1 || list[0].Count != 3 {
		t.Fatalf("liste attendue 1 tracker/3 points, obtenu %+v", list)
	}

	// Vue d'ensemble (sans nom) : cite le tracker, pas le détail.
	ov := trackerView("", "")
	if !strings.Contains(ov, "Abonnés") || strings.Contains(ov, "4210") {
		t.Fatalf("overview inattendu : %s", ov)
	}

	// Squelette (nom seul) : les années présentes.
	sk := trackerView("Abonnés", "")
	if !strings.Contains(sk, "2025") || !strings.Contains(sk, "2026") {
		t.Fatalf("squelette devrait lister les années : %s", sk)
	}

	// Zoom mois : liste les événements du mois, avec leur id.
	m := trackerView("Abonnés", "2026-07")
	if !strings.Contains(m, "4210") || !strings.Contains(m, id) {
		t.Fatalf("vue mois devrait lister l'événement et son id : %s", m)
	}
	// Un autre mois n'apparaît pas.
	if strings.Contains(m, "1000") {
		t.Fatalf("la vue de 2026-07 ne devrait pas contenir un point de 2025 : %s", m)
	}

	// Édition puis suppression.
	if err := trackerEditEvent("abonnes", id, "", "4300"); err != nil {
		t.Fatal(err)
	}
	if s, _ := trackerLoad("abonnes"); s == nil || s.Events[len(s.Events)-1].Text != "4300" {
		t.Fatal("édition non appliquée")
	}
	if err := trackerDeleteEvent("abonnes", id); err != nil {
		t.Fatal(err)
	}
	if s, _ := trackerLoad("abonnes"); s == nil || len(s.Events) != 2 {
		t.Fatal("suppression d'événement non appliquée")
	}
	// Suppression du tracker entier.
	if err := trackerDelete("abonnes"); err != nil {
		t.Fatal(err)
	}
	if len(trackerList()) != 0 {
		t.Fatal("le tracker aurait dû être supprimé")
	}
}
