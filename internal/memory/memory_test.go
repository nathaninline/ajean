package memory

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/tool"
)

// setup donne à chaque test une mémoire vide, sans API configurée.
func setup(t *testing.T) {
	t.Helper()
	t.Setenv("AJEAN_HOME", t.TempDir())
	for _, k := range []string{"AJEAN_API_URL", "AJEAN_API_KEY", "AJEAN_MODEL"} {
		t.Setenv(k, "")
	}
	reset := func() { _ = config.Update(func(c *config.Config) error { *c = config.Config{}; return nil }) }
	reset()
	t.Cleanup(reset)
}

// run appelle un outil de la mémoire comme le ferait le modèle.
func run(t *testing.T, name string, args map[string]any) string {
	t.Helper()
	for _, d := range Tools() {
		if d.Name == name {
			return d.Run(tool.Call{Args: args})
		}
	}
	t.Fatalf("outil %s absent", name)
	return ""
}

func mustOK(t *testing.T, result string) {
	t.Helper()
	if !tool.IsOK(result) {
		t.Fatalf("attendu [ok], reçu : %s", result)
	}
}

// mustNoChange : un résultat qui dit que rien n'a changé ne porte ni « [ok] »
// (l'appelant le compterait comme une écriture) ni « [erreur] ».
func mustNoChange(t *testing.T, result, want string) {
	t.Helper()
	if tool.IsOK(result) || tool.IsErr(result) || !strings.Contains(result, want) {
		t.Fatalf("attendu un résultat sans préfixe contenant %q, reçu : %s", want, result)
	}
}

// unreadable rend un fichier illisible comme le ferait un antivirus ou une
// synchro qui le tient : un dossier prend sa place. Renvoie de quoi le remettre.
func unreadable(t *testing.T, path string) (restore func()) {
	t.Helper()
	aside := path + ".aside"
	existed := os.Rename(path, aside) == nil
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if existed {
			if err := os.Rename(aside, path); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// ficheUses : combien de fois une fiche a été ouverte.
func ficheUses(name string) int {
	mu.Lock()
	defer mu.Unlock()
	usage, _ := usageLocked()
	return usage[name].N
}

func mustErr(t *testing.T, result, want string) {
	t.Helper()
	if !tool.IsErr(result) || !strings.Contains(result, want) {
		t.Fatalf("attendu une erreur contenant %q, reçu : %s", want, result)
	}
}

func TestToolNames(t *testing.T) {
	want := "memory_search memory_remember memory_forget memory_note memory_lesson memory_rule fiche_save fiche_read fiche_patch fiche_merge"
	var got []string
	for _, d := range Tools() {
		got = append(got, d.Name)
		if d.Description == "" || d.Params == nil || d.Run == nil {
			t.Errorf("outil %s incomplet", d.Name)
		}
	}
	if strings.Join(got, " ") != want {
		t.Fatalf("outils : %v", got)
	}
}

func TestProfile(t *testing.T) {
	setup(t)
	mustOK(t, run(t, "memory_remember", map[string]any{"key": "Famille / Mère.Prénom", "value": "Hélène"}))
	mustOK(t, run(t, "memory_remember", map[string]any{"key": "ville", "value": "Nantes"}))
	out := run(t, "memory_remember", map[string]any{"key": "ville", "value": "Lyon"})
	mustOK(t, out)
	if !strings.Contains(out, "mis à jour") {
		t.Errorf("remplacement non signalé : %s", out)
	}
	mustNoChange(t, run(t, "memory_remember", map[string]any{"key": "ville", "value": "Lyon"}), "rien changé")
	facts, err := Profile()
	if err != nil || len(facts) != 2 || facts[0] != (Fact{"famille.mere.prenom", "Hélène"}) || facts[1] != (Fact{"ville", "Lyon"}) {
		t.Fatalf("profil : %v %v", facts, err)
	}
	if entries, _ := Journal("nantes", 0); len(entries) != 1 || entries[0].Kind != kindProfile {
		t.Errorf("l'ancienne valeur devait partir au journal : %v", entries)
	}

	for _, secret := range []string{"mot de passe : Tr0ub4dor", "fakekey0000test1111fake2222test3333", "api key=abcd1234"} {
		mustErr(t, run(t, "memory_remember", map[string]any{"key": "nas.acces", "value": secret}), "refusé")
	}
	mustOK(t, run(t, "memory_remember", map[string]any{"key": "nas.acces", "value": "identifiants dans la fiche serveur-nas"}))
	mustErr(t, run(t, "memory_remember", map[string]any{"key": "long", "value": strings.Repeat("mot ", 100)}), "trop longue")

	var full string
	for i := 0; i < 40 && !tool.IsErr(full); i++ {
		full = run(t, "memory_remember", map[string]any{"key": fmt.Sprintf("fait.%d", i), "value": strings.Repeat("bla ", 60)})
	}
	mustErr(t, full, "profil plein")
	if raw, _ := os.ReadFile(memPath(profileFile)); len(raw) > profileMaxChars {
		t.Errorf("budget dépassé : %d", len(raw))
	}

	mustOK(t, run(t, "memory_forget", map[string]any{"key": "ville"}))
	if err := ForgetFact("ville"); err == nil {
		t.Error("oublier une clé absente devait échouer")
	}
	if !strings.HasPrefix(ContextBlock(), "AJEAN memory") || !strings.Contains(ContextBlock(), "- famille.mere.prenom: Hélène") {
		t.Errorf("bloc de contexte :\n%s", ContextBlock())
	}
}

func TestLessons(t *testing.T) {
	setup(t)
	lesson := func(text string) string { return run(t, "memory_lesson", map[string]any{"text": text}) }
	mustOK(t, lesson("Toujours vérifier la date avant de créer un rappel"))
	mustOK(t, lesson("Répondre en français sauf demande contraire explicite"))
	mustNoChange(t, lesson("toujours vérifier la date avant de créer un rappel"), "déjà connue")
	mustErr(t, lesson("Il faut toujours vérifier la date avant de créer le rappel demandé"), "presque la même chose")

	mustOK(t, run(t, "memory_forget", map[string]any{"key": "lecon-2"}))
	if out := lesson("Ne jamais envoyer de courriel sans confirmation préalable"); !strings.Contains(out, "lecon-3") {
		t.Errorf("le numéro 2 ne devait pas resservir : %s", out)
	}
	if err := ForgetLesson(3); err != nil {
		t.Fatal(err)
	}
	if out := lesson("Les montants se donnent en euros, jamais en dollars"); !strings.Contains(out, "lecon-4") {
		t.Errorf("le numéro 3 ne devait pas resservir : %s", out)
	}
	ls := Lessons()
	if len(ls) != 2 || ls[0].N != 1 || ls[1].N != 4 {
		t.Fatalf("leçons : %v", ls)
	}
	mustErr(t, run(t, "memory_forget", map[string]any{"key": "lecon-2"}), "n'existe pas")

	var full string
	for i := 0; i < 40 && !tool.IsErr(full); i++ {
		full = lesson(fmt.Sprintf("Règle %d : %s", i, strings.Repeat(fmt.Sprintf("terme%dx ", i), 25)))
	}
	mustErr(t, full, "leçons pleines")
	if !strings.Contains(ContextBlock(), "lecon-4: Les montants") {
		t.Errorf("leçons absentes du bloc de contexte")
	}
}

func TestFiches(t *testing.T) {
	setup(t)
	save := func(name, when, content string) string {
		return run(t, "fiche_save", map[string]any{"name": name, "when": when, "content": content})
	}
	mustOK(t, save("Déployer le site", "Quand il faut mettre le site vitrine en production", "1. Lancer les tests unitaires\n2. Pousser la branche principale\n3. Vérifier la page d'accueil"))
	mustErr(t, save("sans-quand", "", "contenu"), "'when' obligatoire")
	mustErr(t, save("mise-en-production-site", "Mettre le site vitrine en production", "Autre procédure"), "couvre déjà")
	mustOK(t, save("recette-crepes", "Quand on veut faire des crêpes bretonnes", "250 g de farine de froment, 3 œufs, un demi-litre de lait entier"))
	const newer = "1. Lancer les tests unitaires\n2. Pousser la branche principale\n3. Vérifier la page d'accueil\n4. Prévenir l'équipe"
	if out := save("deployer-le-site", "Quand il faut mettre le site vitrine en production", newer); !tool.IsOK(out) || !strings.Contains(out, "mise à jour") {
		t.Errorf("même nom : nouvelle version attendue : %s", out)
	}
	mustNoChange(t, save("deployer-le-site", "Quand il faut mettre le site vitrine en production", newer), "rien changé")

	patch := func(old, repl string) string {
		return run(t, "fiche_patch", map[string]any{"name": "deployer-le-site", "old": old, "new": repl})
	}
	mustOK(t, patch("Pousser la branche principale", "Pousser la branche main"))
	mustErr(t, patch("passage absent", "x"), "introuvable")
	mustErr(t, patch("er", "x"), "fois")
	mustErr(t, patch("branche main", "branche main"), "identique")
	f, err := GetFiche("deployer-le-site")
	if err != nil || !strings.Contains(f.Content, "branche main") || strings.Contains(f.Content, "principale") {
		t.Fatalf("patch : %+v %v", f, err)
	}

	mustOK(t, run(t, "memory_lesson", map[string]any{"text": "Le cache du navigateur masque souvent la nouvelle version", "fiche": "deployer-le-site"}))
	if f, _ := GetFiche("deployer-le-site"); !strings.Contains(f.Content, pitfallsHead+"\n- Le cache du navigateur") {
		t.Errorf("piège non ajouté :\n%s", f.Content)
	}
	mustNoChange(t, run(t, "memory_lesson", map[string]any{"text": "Le cache du navigateur masque souvent la nouvelle version", "fiche": "deployer-le-site"}), "rien ajouté")
	mustNoChange(t, run(t, "memory_lesson", map[string]any{"text": "Le cache du navigateur masque souvent la nouvelle version"}), "déjà dit dans la fiche")

	mustOK(t, save("verifier-site", "Contrôles après une livraison", "## Contrôles\n- Tester le formulaire de contact"))
	mustOK(t, run(t, "fiche_merge", map[string]any{"from": "verifier-site", "into": "deployer-le-site"}))
	f, _ = GetFiche("deployer-le-site")
	if !strings.Contains(f.Content, "(ex-fiche verifier-site)") || !strings.Contains(f.Content, "### Contrôles") || !strings.Contains(f.Content, "formulaire de contact") || !strings.Contains(f.When, "aussi : Contrôles après une livraison") {
		t.Fatalf("fusion : %+v", f)
	}
	archived, err := GetFiche("verifier-site")
	if err != nil || !archived.Archived {
		t.Fatalf("la source devait être archivée : %+v %v", archived, err)
	}
	if strings.Contains(ContextBlock(), "- verifier-site:") {
		t.Error("une fiche archivée ne doit plus être dans la liste")
	}

	// Une lecture de fond ne ressort rien des archives et ne compte pas.
	if out, err := ReadFicheQuietly("verifier-site"); err != nil || !strings.Contains(out, "formulaire de contact") {
		t.Fatalf("lecture discrète : %q %v", out, err)
	}
	if f, _ := GetFiche("verifier-site"); !f.Archived {
		t.Error("ReadFicheQuietly a ressorti l'archive")
	}
	if uses := ficheUses("verifier-site"); uses != 0 {
		t.Errorf("ReadFicheQuietly a compté un usage (%d)", uses)
	}

	// La relire pour de bon la ramène dans la liste.
	out := run(t, "fiche_read", map[string]any{"name": "verifier-site"})
	if !strings.Contains(out, "revenue dans ta liste") || !strings.Contains(out, "formulaire de contact") {
		t.Fatalf("relecture d'une archive : %s", out)
	}
	if f, _ := GetFiche("verifier-site"); f.Archived {
		t.Error("la fiche relue devait sortir des archives")
	}
	if uses := ficheUses("verifier-site"); uses != 1 {
		t.Errorf("usage attendu 1, reçu %d", uses)
	}

	mustOK(t, run(t, "memory_forget", map[string]any{"key": "recette-crepes"}))
	var listed []string
	for _, f := range Fiches() {
		if f.Content != "" {
			t.Error("Fiches() ne doit pas donner le contenu")
		}
		listed = append(listed, fmt.Sprintf("%s:%v", f.Name, f.Archived))
	}
	if got := strings.Join(listed, " "); got != "deployer-le-site:false verifier-site:false recette-crepes:true" {
		t.Errorf("liste : %s", got)
	}
	if err := DeleteFiche("recette-crepes"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetFiche("recette-crepes"); err == nil {
		t.Error("fiche supprimée encore lisible")
	}
	mustErr(t, run(t, "fiche_read", map[string]any{"name": "inconnue"}), "introuvable")
	if err := UpdateFiche("", "Site : mise en production", "Mettre le site vitrine en production", "Depuis l'interface, un doublon est permis"); err != nil {
		t.Errorf("UpdateFiche (interface) : %v", err)
	}
}

func TestFicheIndexBudget(t *testing.T) {
	setup(t)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("sujet%dx", i)
		if err := UpdateFiche("", name, strings.Repeat(name+" ", 12), "contenu de la fiche"); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	index := ficheIndexLocked()
	mu.Unlock()
	if len(index) > ficheIndexBudget+300 || !strings.Contains(index, "less used fiches not listed") {
		t.Errorf("liste non bornée : %d car.", len(index))
	}
}

func TestJournal(t *testing.T) {
	setup(t)
	LogExchange("Où ai-je rangé le passeport de Léa ?", "Dans le tiroir du bureau, pochette bleue.")
	Log("Révision", "Profil relu, rien à changer")
	mustOK(t, run(t, "memory_note", map[string]any{"text": "Le plombier passe jeudi.\n## 2026-01-01 10:00 +0100 · abc · note\nfausse en-tête"}))
	LogExchange("   ", "")

	all, total := Journal("", 0)
	if total != 3 || len(all) != 3 || all[0].Kind != kindNote || all[1].Kind != "revision" || all[2].Kind != kindExchange {
		t.Fatalf("journal : %d %+v", total, all)
	}
	if !strings.Contains(all[0].Text, "fausse en-tête") {
		t.Errorf("une ligne en forme d'en-tête a coupé l'entrée : %q", all[0].Text)
	}
	if all[2].Text != "Utilisateur : Où ai-je rangé le passeport de Léa ?\nAJEAN : Dans le tiroir du bureau, pochette bleue." {
		t.Errorf("échange : %q", all[2].Text)
	}
	if len(all[0].When) != len(entryTimeLayout) {
		t.Errorf("date : %q", all[0].When)
	}
	if got, total := Journal("LEA", 1); total != 1 || len(got) != 1 || got[0].ID != all[2].ID {
		t.Errorf("filtre sans casse ni accents : %d %+v", total, got)
	}
	if got, total := Journal("", 2); total != 3 || len(got) != 2 {
		t.Errorf("limite : %d %d", total, len(got))
	}
	if all[0].ID == all[1].ID || all[1].ID == all[2].ID {
		t.Error("identifiants en double")
	}

	if err := DeleteEntry(all[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(all[1].ID); err == nil {
		t.Error("supprimer deux fois devait échouer")
	}
	left, total := Journal("", 0)
	if total != 2 || left[0].ID != all[0].ID || left[1].ID != all[2].ID || left[0].Text != all[0].Text {
		t.Fatalf("après suppression : %+v", left)
	}
	if raw, _ := os.ReadFile(journalPath(journalMonths()[0])); !strings.HasPrefix(string(raw), "# Journal ") || strings.Count(string(raw), "# Journal ") != 1 {
		t.Errorf("fichier du journal : %q", raw)
	}

	// Un message fait seulement de pièces jointes laisse quand même sa réponse.
	LogExchange("", "Voici le résumé du document joint.")
	if got, _ := Journal("", 1); len(got) != 1 || got[0].Text != "Utilisateur : (pièces jointes seulement)\nAJEAN : Voici le résumé du document joint." {
		t.Errorf("échange sans texte : %+v", got)
	}
}

func TestSearchWordsAndExactTokens(t *testing.T) {
	setup(t)
	LogExchange("L'installation échoue avec le code 0x80004005 sur le portable", "Essayons de relancer le service.")
	LogExchange("Cette fois la mise à jour échoue avec 0x80070005", "C'est un refus d'accès : lance l'installateur en administrateur.")
	LogExchange("Quel cadeau pour l'anniversaire de Marie ?", "Un collier en argent, comme convenu avec Paul.")
	mustOK(t, run(t, "memory_note", map[string]any{"text": "Le script sauvegarde.ps1 tourne chaque dimanche soir"}))
	if err := UpdateFiche("", "cadeaux-famille", "Idées de cadeaux pour la famille", "Marie aime les bijoux en argent"); err != nil {
		t.Fatal(err)
	}

	search := func(q string) string { return run(t, "memory_search", map[string]any{"query": q}) }
	lines := func(out string) []string { return strings.Split(out, "\n") }

	out := search("Quel cadeau pour l'anniversaire de Marie ? collier argent")
	if got := lines(out); len(got) < 2 || !strings.HasPrefix(got[0], "- [fiche] cadeaux-famille") || !strings.HasSuffix(got[0], "(fiche_read pour l'ouvrir)") || !strings.Contains(got[1], "anniversaire de Marie") {
		t.Errorf("fiche d'abord, puis l'échange :\n%s", out)
	}
	out = search("l'erreur 0x80070005 pendant la mise à jour")
	if got := lines(out); !strings.Contains(got[0], "0x80070005") {
		t.Errorf("le jeton exact devait passer devant :\n%s", out)
	}
	out = search("sauvegarde.ps1")
	if got := lines(out); len(got) != 1 || !strings.Contains(got[0], "· note]") {
		t.Errorf("nom de fichier :\n%s", out)
	}
	if out := search("zzzz introuvable"); !strings.HasPrefix(out, "[aucun résultat]") {
		t.Errorf("rien attendu : %s", out)
	}
	mustErr(t, search("  "), "requête vide")

	if got := exactTokens("Le fichier app.py plante avec ECONNREFUSED sur 192.168.1.10, à 20km d'ici (2ème essai)."); strings.Join(got, " ") != "app.py econnrefused 192.168.1.10" {
		t.Errorf("jetons exacts : %v", got)
	}
}

func TestDetectCorrection(t *testing.T) {
	cases := map[string]Correction{
		"Merci, c'est parfait":                       NoCorrection,
		"Peux-tu réserver une table pour demain ?":   NoCorrection,
		"Non, pas comme ça, je voulais un tableau":   Corrected,
		"À partir de maintenant, écris en vouvoyant": Corrected,
		"C’est faux, relis le document":              Corrected,
		"Je te l'ai déjà dit : pas de tirets":        CorrectedAgain,
		"Tu recommences avec les majuscules !":       CorrectedAgain,
		"I already told you, no emojis":              CorrectedAgain,
	}
	for text, want := range cases {
		if got := DetectCorrection(text); got != want {
			t.Errorf("%q : %d, attendu %d", text, got, want)
		}
	}
}

func TestConcurrentWrites(t *testing.T) {
	setup(t)
	const workers, rounds = 8, 15
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				LogExchange(fmt.Sprintf("message %d du fil %d sur les vacances", i, w), "réponse")
				if err := UpdateFact("", fmt.Sprintf("fil%d.compteur", w), fmt.Sprint(i)); err != nil {
					t.Error(err)
				}
				if err := UpdateFiche("", fmt.Sprintf("fil%d-notes", w), "Notes du fil", fmt.Sprintf("tour %d", i)); err != nil {
					t.Error(err)
				}
				run(t, "memory_search", map[string]any{"query": "vacances"})
				_ = ContextBlock()
				_ = Recall(t.Context(), "que disait-on des vacances", nil)
				_ = LastChange()
			}
		}()
	}
	wg.Wait()
	if _, total := Journal("message", 0); total != workers*rounds {
		t.Errorf("échanges consignés : %d, attendu %d", total, workers*rounds)
	}
	facts, err := Profile()
	if err != nil || len(facts) != workers {
		t.Errorf("profil : %d faits, %v", len(facts), err)
	}
	for _, f := range facts {
		if f.Value != fmt.Sprint(rounds-1) {
			t.Errorf("%s = %s", f.Key, f.Value)
		}
	}
}

// Depuis l'interface, « renommer » vers un nom qui se normalise comme l'ancien
// n'est pas un renommage : la fiche ne doit pas être supprimée après l'écriture.
func TestUpdateFicheRename(t *testing.T) {
	setup(t)
	const when = "Quand il faut mettre le site en production"
	if err := UpdateFiche("", "deployer-le-site", when, "Lancer les tests"); err != nil {
		t.Fatal(err)
	}
	run(t, "fiche_read", map[string]any{"name": "deployer-le-site"})
	if err := UpdateFiche("deployer-le-site", "Deployer le site", when, "Lancer les tests, puis pousser"); err != nil {
		t.Fatal(err)
	}
	if f, err := GetFiche("deployer-le-site"); err != nil || f.Content != "Lancer les tests, puis pousser" {
		t.Fatalf("la fiche a disparu en changeant la casse de son nom : %+v %v", f, err)
	}

	// Une écriture refusée ne retire pas l'ancienne fiche.
	if err := UpdateFiche("deployer-le-site", "mise-en-ligne", when, "   "); err == nil {
		t.Fatal("contenu vide accepté")
	}
	if err := UpdateFiche("", "autre-fiche", "Pour autre chose", "Contenu de l'autre"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateFiche("deployer-le-site", "autre-fiche", when, "Écraserait l'autre"); err == nil {
		t.Fatal("renommer vers une fiche existante devait être refusé")
	}
	for name, want := range map[string]string{"deployer-le-site": "Lancer les tests, puis pousser", "autre-fiche": "Contenu de l'autre"} {
		if f, err := GetFiche(name); err != nil || f.Content != want {
			t.Errorf("%s après un renommage refusé : %+v %v", name, f, err)
		}
	}

	// Un vrai renommage : l'ancien nom disparaît, l'usage suit.
	if err := UpdateFiche("deployer-le-site", "Mise en ligne", when, "Lancer les tests, puis pousser"); err != nil {
		t.Fatal(err)
	}
	if _, err := GetFiche("deployer-le-site"); err == nil {
		t.Error("l'ancien nom existe encore")
	}
	if f, err := GetFiche("mise-en-ligne"); err != nil || f.When != when {
		t.Errorf("fiche renommée : %+v %v", f, err)
	}
	if ficheUses("mise-en-ligne") != 1 || ficheUses("deployer-le-site") != 0 {
		t.Error("l'usage devait suivre la fiche renommée")
	}
}

func TestUpdateFactRename(t *testing.T) {
	setup(t)
	for _, err := range []error{UpdateFact("", "famille.mere", "Hélène"), UpdateFact("", "ville", "Nantes")} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := UpdateFact("famille.mere", "Famille.Mere", "Hélène, née en 1961"); err != nil {
		t.Fatal(err)
	}
	facts, _ := Profile()
	if len(facts) != 2 || facts[0] != (Fact{"famille.mere", "Hélène, née en 1961"}) {
		t.Fatalf("la ligne a disparu en changeant la casse de sa clé : %v", facts)
	}

	// Une écriture refusée ne retire pas l'ancienne ligne.
	if err := UpdateFact("famille.mere", "famille.maman", "mot de passe : Tr0ub4dor"); err == nil {
		t.Fatal("secret accepté")
	}
	if facts, _ := Profile(); len(facts) != 2 || facts[0].Key != "famille.mere" {
		t.Fatalf("profil après un renommage refusé : %v", facts)
	}

	// Un vrai renommage : une seule écriture, la ligne garde sa place.
	if err := UpdateFact("Famille / Mère", "famille.maman", "Hélène"); err != nil {
		t.Fatal(err)
	}
	facts, _ = Profile()
	if len(facts) != 2 || facts[0] != (Fact{"famille.maman", "Hélène"}) || facts[1].Key != "ville" {
		t.Errorf("profil : %v", facts)
	}
	if got, _ := Journal("Avant : Hélène, née en 1961", 0); len(got) != 1 {
		t.Errorf("l'ancienne valeur devait rester dans le journal : %+v", got)
	}

	// Renommer vers une clé qui existe la remplace ; son ancienne valeur part au journal.
	if err := UpdateFact("famille.maman", "ville", "Rennes"); err != nil {
		t.Fatal(err)
	}
	if facts, _ = Profile(); len(facts) != 1 || facts[0] != (Fact{"ville", "Rennes"}) {
		t.Errorf("profil : %v", facts)
	}
	if got, _ := Journal("nantes", 0); len(got) != 1 {
		t.Errorf("la valeur remplacée devait rester dans le journal : %+v", got)
	}
}

// Une fiche archivée n'est jamais perdue : ni écrasée par une autre archive du
// même nom, ni doublée par une fiche active.
func TestArchivesAreNeverOverwritten(t *testing.T) {
	setup(t)
	save := func(content string) string {
		return run(t, "fiche_save", map[string]any{"name": "taxe-fonciere", "when": "Quand arrive l'avis de taxe foncière", "content": content})
	}
	listed := func() string {
		var out []string
		for _, f := range Fiches() {
			out = append(out, fmt.Sprintf("%s:%v", f.Name, f.Archived))
		}
		return strings.Join(out, " ")
	}
	mustOK(t, save("Payer en ligne avant le quinze octobre"))
	mustOK(t, run(t, "memory_forget", map[string]any{"key": "taxe-fonciere"}))

	// Réenregistrer sous le nom d'une archive la ressort : une seule fiche.
	if out := save("Mensualisation mise en place, plus rien à payer à la main"); !tool.IsOK(out) || !strings.Contains(out, "archivée") {
		t.Fatalf("la fiche archivée devait être ressortie : %s", out)
	}
	if got := listed(); got != "taxe-fonciere:false" {
		t.Fatalf("fiches : %s", got)
	}

	// Depuis l'interface aussi.
	mustOK(t, run(t, "memory_forget", map[string]any{"key": "taxe-fonciere"}))
	if err := UpdateFiche("taxe-fonciere", "taxe-fonciere", "Quand arrive l'avis de taxe foncière", "Version revue par l'utilisateur"); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got != "taxe-fonciere:false" {
		t.Fatalf("fiches après modification d'une archive dans l'interface : %s", got)
	}

	// Une archive et une fiche active du même nom (mémoire d'avant, fichier
	// posé à la main) : archiver la seconde garde les deux.
	if err := home.WriteFile(fichePath("taxe-fonciere", true), []byte(renderFiche(Fiche{Name: "taxe-fonciere", When: "Ancienne", Content: "Chèque à envoyer au centre des impôts"}))); err != nil {
		t.Fatal(err)
	}
	mustOK(t, run(t, "memory_forget", map[string]any{"key": "taxe-fonciere"}))
	if got := listed(); got != "taxe-fonciere:true taxe-fonciere-2:true" {
		t.Fatalf("archives : %s", got)
	}
	old, _ := GetFiche("taxe-fonciere")
	recent, _ := GetFiche("taxe-fonciere-2")
	if !strings.Contains(old.Content, "Chèque") || !strings.Contains(recent.Content, "Version revue") {
		t.Errorf("une archive a été écrasée : %q / %q", old.Content, recent.Content)
	}
}

// Une lecture ratée n'est pas un fichier vide : on n'écrit jamais par-dessus.
func TestUnreadableFilesAreNotRewritten(t *testing.T) {
	setup(t)
	mustOK(t, run(t, "memory_lesson", map[string]any{"text": "Toujours vérifier la date avant de créer un rappel"}))
	mustOK(t, run(t, "memory_rule", map[string]any{"from": "—", "to": ", "}))

	restore := unreadable(t, memPath(lessonsFile))
	mustErr(t, run(t, "memory_lesson", map[string]any{"text": "Répondre en français sauf demande contraire explicite"}), "leçons illisibles")
	if err := ForgetLesson(1); err == nil || !strings.Contains(err.Error(), "illisibles") {
		t.Errorf("ForgetLesson sur un fichier illisible : %v", err)
	}
	if !strings.Contains(ContextBlock(), "lessons unavailable") {
		t.Errorf("le bloc de contexte devait signaler les leçons illisibles :\n%s", ContextBlock())
	}
	restore()
	if ls := Lessons(); len(ls) != 1 || ls[0].N != 1 {
		t.Errorf("leçons après la lecture ratée : %v", ls)
	}

	restore = unreadable(t, memPath(rulesFile))
	mustErr(t, run(t, "memory_rule", map[string]any{"from": "mail", "to": "courriel"}), "règles illisibles")
	if err := ForgetRule(1); err == nil || !strings.Contains(err.Error(), "illisibles") {
		t.Errorf("ForgetRule sur un fichier illisible : %v", err)
	}
	if got := ApplyRules("a — b"); got != "a — b" {
		t.Errorf("règles illisibles : le texte passe tel quel, reçu %q", got)
	}
	restore()
	if rs := Rules(); len(rs) != 1 || rs[0].From != "—" {
		t.Errorf("règles après la lecture ratée : %v", rs)
	}
	mustNoChange(t, run(t, "memory_rule", map[string]any{"from": "—", "to": ", "}), "rien changé")

	// Des compteurs d'usage illisibles ne repartent pas de zéro.
	if err := UpdateFiche("", "essai", "Pour un essai", "contenu"); err != nil {
		t.Fatal(err)
	}
	run(t, "fiche_read", map[string]any{"name": "essai"})
	restore = unreadable(t, usagePath())
	run(t, "fiche_read", map[string]any{"name": "essai"})
	restore()
	if ficheUses("essai") != 1 {
		t.Errorf("usage après une lecture ratée : %d", ficheUses("essai"))
	}
}

func TestPitfallSectionIsFoundByItsExactLine(t *testing.T) {
	setup(t)
	if err := UpdateFiche("", "serveur-media", "Quand le serveur multimédia ne répond plus", "Redémarrer le conteneur principal"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateFiche("", "vieux-serveur", "Ancienne procédure du serveur multimédia", "## Pièges\n- Le disque externe met une minute à monter"); err != nil {
		t.Fatal(err)
	}
	mustOK(t, run(t, "fiche_merge", map[string]any{"from": "vieux-serveur", "into": "serveur-media"}))
	mustOK(t, run(t, "memory_lesson", map[string]any{"text": "Attendre la fin de la bibliothèque avant de relancer", "fiche": "serveur-media"}))
	f, _ := GetFiche("serveur-media")
	if !strings.HasSuffix(f.Content, "\n\n"+pitfallsHead+"\n- Attendre la fin de la bibliothèque avant de relancer") {
		t.Fatalf("le piège devait ouvrir la section de la fiche, pas rejoindre le « ### Pièges » de la fiche absorbée :\n%s", f.Content)
	}
	// Le suivant rejoint la section, avant le titre qui la suit.
	got := withPitfall("Étapes\n\n## Pièges\n- un\n\n## Suite\nfin", "deux")
	if got != "Étapes\n\n## Pièges\n- un\n- deux\n\n## Suite\nfin" {
		t.Errorf("piège mal placé :\n%s", got)
	}
}

func TestMergeLeavesCodeBlocksAlone(t *testing.T) {
	const fence = "```"
	src := "# Titre\n" + fence + "sh\n# commentaire shell\nrsync -a src dst\n" + fence + "\n## Suite\n~~~\n# autre commentaire\n~~~"
	want := "## Titre\n" + fence + "sh\n# commentaire shell\nrsync -a src dst\n" + fence + "\n### Suite\n~~~\n# autre commentaire\n~~~"
	if got := demoteHeadings(src); got != want {
		t.Errorf("titres descendus :\n%s\n--- attendu ---\n%s", got, want)
	}
}

// La liste des fiches vient d'un cache : il doit voir toute écriture, même de
// même taille dans le même instant, et tout changement fait hors du programme.
func TestFicheCacheSeesEveryChange(t *testing.T) {
	setup(t)
	for _, when := range []string{"Pour le sujet A", "Pour le sujet B", "Pour le sujet C"} {
		if err := UpdateFiche("", "essai", when, "contenu"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ContextBlock(), "- essai: "+when) {
			t.Fatalf("liste périmée après « %s » :\n%s", when, ContextBlock())
		}
	}
	if err := os.WriteFile(fichePath("essai", false), []byte("# essai\nwhen: Modifiée à la main, plus longue\n\ncontenu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ContextBlock(), "- essai: Modifiée à la main") {
		t.Errorf("modification hors du programme non vue :\n%s", ContextBlock())
	}
	if err := DeleteFiche("essai"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ContextBlock(), "- essai:") {
		t.Error("fiche supprimée encore dans la liste")
	}
}

func TestJournalAppendsWithoutRewriting(t *testing.T) {
	setup(t)
	Log("note", "première entrée")
	path := journalPath(journalMonths()[0])
	// Ce qu'un utilisateur ajoute à la main, sans fin de ligne, reste en place.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("ligne ajoutée à la main"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	Log("note", "deuxième entrée")
	all, total := Journal("", 0)
	if total != 2 || all[0].Text != "deuxième entrée" || !strings.Contains(all[1].Text, "ligne ajoutée à la main") {
		t.Errorf("journal : %+v", all)
	}
	raw, _ := os.ReadFile(path)
	if strings.Count(string(raw), "# Journal ") != 1 {
		t.Errorf("l'en-tête du mois ne s'écrit qu'une fois :\n%s", raw)
	}
}
