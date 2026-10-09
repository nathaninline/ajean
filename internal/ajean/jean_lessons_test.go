package ajean

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJeanLessons(t *testing.T) {
	testHome(t)

	// Leçon générale : injectée, numérotée, sans doublon, oubliable.
	if _, err := JeanLesson("L'horloge du serveur est en UTC : ne jamais s'en servir pour l'heure", ""); err != nil {
		t.Fatal(err)
	}
	if out, _ := JeanLesson("l'horloge du serveur est en UTC : ne jamais s'en servir pour l'heure", ""); !strings.Contains(out, "déjà") {
		t.Fatalf("doublon accepté : %q", out)
	}
	if _, err := JeanLesson("Alice veut des réponses sans tiret cadratin", ""); err != nil {
		t.Fatal(err)
	}
	ctx := jeanContextMessage().Content.(string)
	if !strings.Contains(ctx, "lecon-2: Alice veut") {
		t.Fatalf("leçons absentes du contexte :\n%s", ctx)
	}
	if _, err := JeanForget("lecon-1"); err != nil {
		t.Fatal(err)
	}
	// Les numéros ne bougent pas : lecon-2 reste lecon-2, la suivante sera lecon-3.
	if ls, _ := jeanLessons(); len(ls) != 1 || ls[0].N != 2 || !strings.HasPrefix(ls[0].Text, "Alice") {
		t.Fatalf("leçons après oubli = %v", ls)
	}
	if out, _ := JeanLesson("Toujours vérifier la liste après écriture", ""); !strings.Contains(out, "lecon-3") {
		t.Fatalf("nouvelle leçon : %q, veut lecon-3", out)
	}
	if _, err := JeanForget("lecon-1"); err == nil {
		t.Fatal("lecon-1 déjà oubliée : doit échouer")
	}
	if _, err := JeanForget("lecon-3"); err != nil {
		t.Fatal(err)
	}
	// Ancien format sans numéro : numéroté à la lecture.
	if ls := parseJeanLessons("- a\n- [lecon-5] b\n- c\n"); len(ls) != 3 || ls[0].N != 1 || ls[1].N != 5 || ls[2].N != 6 {
		t.Fatalf("parse = %+v", ls)
	}

	// Budget dur.
	var full bool
	for i := 0; i < 30; i++ {
		if _, err := JeanLesson(strings.Repeat("y", 200)+string(rune('a'+i)), ""); err != nil {
			full = strings.Contains(err.Error(), "leçons pleines")
			break
		}
	}
	if !full {
		t.Fatal("les leçons auraient dû refuser au-delà du budget")
	}

	// Leçon de fiche : rangée dans « Pièges », section créée puis complétée.
	if _, err := JeanSave("deployer", "pour déployer le serveur", "1. build\n2. copier\n\n## Notes\nrien"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanLesson("Arrêter le service avant de copier le binaire", "deployer"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanLesson("Vérifier la version après redémarrage", "Déployer"); err != nil {
		t.Fatal(err)
	}
	f, _ := JeanRead("deployer")
	if !strings.Contains(f, "## Pièges\n- Arrêter le service avant de copier le binaire\n- Vérifier la version") {
		t.Fatalf("pièges mal rangés :\n%s", f)
	}

	// jean_patch : remplacement exact et unique.
	if _, err := JeanPatch("deployer", "2. copier", "2. arrêter\n3. copier"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanPatch("deployer", "introuvable", "x"); err == nil {
		t.Fatal("passage absent accepté")
	}
	if f, _ = JeanRead("deployer"); !strings.Contains(f, "2. arrêter\n3. copier") || !strings.Contains(f, "when: pour déployer") {
		t.Fatalf("patch raté :\n%s", f)
	}
}

func TestJeanReflectGuard(t *testing.T) {
	ctx := context.WithValue(context.Background(), jeanReflectKey{}, true)
	if !isJeanReflecting(ctx) || isJeanReflecting(context.Background()) {
		t.Fatal("détection du tour de révision")
	}
	for _, n := range []string{"bash", "write", "task_create", "web_open", "jean_projects"} {
		if jeanReflectAllowed(n) {
			t.Fatalf("%s ne doit pas être permis en révision", n)
		}
	}
	if !jeanReflectAllowed("jean_lesson") || !jeanReflectAllowed("jean_remember") {
		t.Fatal("les outils de mémoire doivent être permis")
	}
}

func TestJeanCorrectionKind(t *testing.T) {
	cases := map[string]int{
		"[vendredi 2026-10-02 20:00 (Europe/Paris)] Non, je voulais une majuscule au début seulement": jeanCorrection,
		"Je t'ai déjà dit de ne pas mettre de tirets cadratins !":                                     jeanCorrRepeat,
		"Encore une fois, la liste doit être triée.":                                                  jeanCorrRepeat,
		"À partir de maintenant, réponds en une phrase.":                                              jeanCorrection,
		"Tu t'es trompé dans le total.":                                                               jeanCorrection,
		"Salut, tu peux me faire un résumé de l'actu ?":                                               jeanCorrNone,
		"Ajoute du lait à ma liste":                                                                   jeanCorrNone,
		"Merci, c'est parfait":                                                                        jeanCorrNone,
	}
	for in, want := range cases {
		if got := jeanCorrectionKind(in); got != want {
			t.Errorf("%q : %d, veut %d", in, got, want)
		}
	}
	if !strings.Contains(jeanReflectPrompt(2, jeanCorrRepeat, []string{"je t'ai déjà dit"}), "ALREADY") {
		t.Fatal("brief d'erreur répétée absent")
	}
}

func TestJeanConsolidateGuard(t *testing.T) {
	testHome(t)
	_, _ = JeanRemember("sante.cafe", "Plus de café après 14h, ordre du médecin")
	_, _ = JeanRemember("ville", "Lyon")
	_, _ = JeanLesson("Les montants se calculent avec budget.py, jamais de tête", "")
	_, _ = JeanLesson("La liste de courses se trie en ignorant les accents", "")
	_, _ = JeanSave("courses", "liste de courses", "1. lire courses.txt\n2. ajouter l'article avec une majuscule initiale\n3. réafficher la liste complète")
	before := jeanSnapMemory()

	// Une « consolidation » qui fusionne bien une leçon (déplacée dans la fiche)…
	_, _ = JeanLesson("La liste de courses se trie en ignorant les accents (Œufs après Lait)", "courses")
	_, _ = JeanForget("lecon-2")
	// … mais en perd une autre, abîme la fiche et remplace une valeur du profil.
	_, _ = JeanForget("lecon-1")
	_, _ = JeanPatch("courses", "2. ajouter l'article avec une majuscule initiale\n", "")
	_, _ = JeanRemember("ville", "Paris")

	restored := jeanGuardCheck(before)
	ls, _ := jeanLessons()
	var texts []string
	for _, l := range ls {
		texts = append(texts, l.Text)
	}
	if len(ls) != 1 || !strings.Contains(ls[0].Text, "budget.py") {
		t.Fatalf("leçon perdue non restaurée (ou fusion annulée) : %v / %v", texts, restored)
	}
	if f, _ := JeanRead("courses"); !strings.Contains(f, "majuscule initiale") || !strings.Contains(f, "Œufs après Lait") {
		t.Fatalf("fiche abîmée non restaurée :\n%s", f)
	}
	facts, _ := jeanProfile()
	for _, f := range facts {
		if f.Key == "ville" && f.Value != "Paris" {
			t.Fatalf("remplacement voulu annulé : %v", f)
		}
	}
}

func TestJeanFicheLife(t *testing.T) {
	testHome(t)
	for i := 0; i < 80; i++ {
		if _, err := JeanSave(fmt.Sprintf("procedure-%02d", i), fmt.Sprintf("sujet%02d theme%02d domaine%02d", i, i, i), "étapes"); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = JeanRead("procedure-03") // une fiche ancienne mais qui sert
	idx := jeanFicheIndex()
	if len(idx) > jeanFicheIndexBudget+400 {
		t.Fatalf("index hors budget : %d car.", len(idx))
	}
	if !strings.Contains(idx, "procedure-03:") || !strings.Contains(idx, "less used fiches not listed") {
		t.Fatalf("la fiche utilisée doit passer devant et le reste être signalé :\n%s", idx)
	}
	// Oublier archive : hors liste, trouvable, et la rouvrir la ramène.
	if out, err := JeanForget("procedure-03"); err != nil || !strings.Contains(out, "archivée") {
		t.Fatalf("archive : %q %v", out, err)
	}
	if strings.Contains(jeanFicheIndex(), "procedure-03:") {
		t.Fatal("fiche archivée encore listée")
	}
	if hits := jeanFicheHits([]string{"sujet03"}); !strings.Contains(strings.Join(hits, "\n"), "[fiche archivée] procedure-03") {
		t.Fatalf("archivée introuvable : %v", hits)
	}
	if out, err := JeanRead("procedure-03"); err != nil || !strings.Contains(out, "back in your list") {
		t.Fatalf("retour d'archive : %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(jeanDir(), ficheFile("procedure-03"))); err != nil {
		t.Fatal("la fiche rouverte doit revenir dans la liste")
	}
}

func TestJeanLessonNumbersNeverReused(t *testing.T) {
	testHome(t)
	_, _ = JeanLesson("première règle", "")
	_, _ = JeanForget("lecon-1")
	out, _ := JeanLesson("deuxième règle", "")
	if !strings.Contains(out, "lecon-2") {
		t.Fatalf("numéro réutilisé : %q", out)
	}
}

func TestJeanHygiene(t *testing.T) {
	for v, want := range map[string]bool{
		"Serveur média 10.0.0.5, identifiants alice / Hunter2024":              true,
		"Boîte mail : alice@example.com, mdp Hunter2024+":                      true,
		"clé 9f1c0b7e4a2d6835e0c7b19af3d42e86b5a0c1d7e39f84a26b0d5c1e7f3a9b24": true,
		"Serveur média 10.0.0.5, identifiants dans la fiche media-server":      false,
		"Alice aime les films d'action et de science-fiction":                  false,
	} {
		if got := jeanLooksSecret(v); got != want {
			t.Errorf("jeanLooksSecret(%q) = %v", v, got)
		}
	}
	in := "[Saturday 2026-10-03 09:06 (Europe/Paris)] Images jointes à ce message, que tu vois directement ci-dessous : ne les rouvre PAS.\n- uploads/IMG_3516.png (2 Mo)\n- uploads/IMG_3517.png (1 Mo)\n\n[Saturday 2026-10-03 09:06 (Europe/Paris)] Prends-en connaissance."
	if got := jeanCleanUserText(in); got != "[image : IMG_3516.png, IMG_3517.png] Prends-en connaissance." {
		t.Errorf("jeanCleanUserText = %q", got)
	}
	msgs := []Message{{Role: "user", Content: "Trouve la machine"}, {Role: "assistant", Content: ""}, {Role: "user", Content: []map[string]any{{"type": "text", "text": "Image :"}}}}
	if got := jeanTurnUserText(msgs); got != "Trouve la machine" {
		t.Errorf("jeanTurnUserText = %q", got)
	}
}

func TestJeanPitfallRouting(t *testing.T) {
	testHome(t)
	JeanSave("acme-api-test", "Alice demande de tester une clé API Acme, de configurer l'API Acme, ou de chercher des torrents sur Acme", "# API\n\n## Pièges\n- acme.example résout vers 127.0.0.1 ici : passer par curl --resolve")
	JeanSave("films-recents-acme", "Alice demande de trouver des films récents/à la mode dispo sur Acme, ou y a quoi de bon à voir", "# Films\n\n## Pièges\n- rien")
	JeanSave("youtube-stats", "Alice demande les stats (vues, likes, commentaires) de ses vidéos YouTube", "# YT")
	p := "Quand Alice demande des films récents 2026 sur Acme, vérifier que le film est vraiment une sortie 2026 avant de le proposer."
	out, _ := JeanLesson(p, "acme-api-test")
	if !strings.Contains(out, "films-recents-acme") || !strings.HasPrefix(out, "[refusé]") {
		t.Fatalf("mauvaise fiche acceptée : %q", out)
	}
	if out, _ := JeanLesson(p, "acme-api-test"); !strings.Contains(out, "piège ajouté") {
		t.Fatalf("confirmation refusée : %q", out)
	}
	if out, _ := JeanLesson("acme.example résout vers 127.0.0.1 sur cette machine : passer par curl --resolve", ""); !strings.Contains(out, "déjà dit") {
		t.Fatalf("doublon d'une fiche accepté en leçon : %q", out)
	}
}

func TestJeanAnswerThenSave(t *testing.T) {
	long := strings.Repeat("Tu avais raison, le bug est déjà corrigé. ", 5)
	tc := func(n string) ToolCall { var c ToolCall; c.Function.Name = n; return c }
	ok := Message{Role: "assistant", Content: long, ToolCalls: []ToolCall{tc("jean_patch")}}
	if !jeanAnswerThenSave(ok, []Message{{Role: "tool", Content: "[ok] fiche modifiée"}}) {
		t.Fatal("réponse + rangement : le tour aurait dû s'arrêter")
	}
	if jeanAnswerThenSave(ok, []Message{{Role: "tool", Content: "[erreur] leçon trop longue"}}) {
		t.Fatal("écriture en erreur : le modèle doit pouvoir la corriger")
	}
	if jeanAnswerThenSave(Message{Role: "assistant", Content: long, ToolCalls: []ToolCall{tc("bash")}}, nil) {
		t.Fatal("un vrai outil n'est pas un rangement")
	}
	if jeanAnswerThenSave(Message{Role: "assistant", Content: "Je note ça.", ToolCalls: []ToolCall{tc("jean_save")}}, nil) {
		t.Fatal("texte court : ce n'est pas une réponse")
	}
}

func TestJeanStaleChange(t *testing.T) {
	cur := "### fiche media-server\n- Jellyfin : un 401 sur /Users/Authenticate veut dire identifiants refusés tant qu'on n'a pas prouvé le contraire"
	old := "Fiche « media-server », piège ajouté : Jellyfin 10.0.0.5 : le mot de passe est bien alice/Hunter2024 (confirmé par Alice) même si l'API renvoie 401. Ne pas douter des identifiants, c'est un bug côté Jellyfin."
	if !jeanStaleChange(old, cur) {
		t.Fatal("un piège retiré depuis aurait dû être marqué")
	}
	still := "Fiche « media-server », piège ajouté : Jellyfin : un 401 sur /Users/Authenticate veut dire identifiants refusés tant qu'on n'a pas prouvé le contraire"
	if jeanStaleChange(still, cur) {
		t.Fatal("un piège toujours présent ne doit pas être marqué")
	}
	if jeanStaleChange("Fiche « media-server » retouchée", cur) {
		t.Fatal("une entrée sans contenu ne doit pas être marquée")
	}
}
