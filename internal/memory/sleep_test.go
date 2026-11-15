package memory

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"ajean/internal/home"
)

const (
	nasWhen   = "Quand il faut sauvegarder le serveur de fichiers"
	nasStep1  = "1. Brancher le disque externe Toshiba sur le port arrière"
	nasStep2  = "2. Lancer rsync avec les options archive et delete"
	nasCheck  = "- Vérifier le voyant orange avant de débrancher"
	nasFiche  = "sauvegarde-nas"
	wifiFiche = "reseau-wifi"
)

// sleepCall fait un appel d'outil qui doit réussir, comme le ménage en fait
// pendant un sommeil.
func sleepCall(t *testing.T, name string, args map[string]any) {
	t.Helper()
	mustOK(t, run(t, name, args))
}

// beginSleep ouvre un sommeil que le test fermera, même s'il échoue avant :
// un sommeil resté ouvert bloquerait les tests suivants.
func beginSleep(t *testing.T) *Sleep {
	t.Helper()
	s, err := BeginSleep()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Finish("interrompu", "") })
	return s
}

func seedSleepMemory(t *testing.T) {
	t.Helper()
	setup(t)
	for _, err := range []error{
		UpdateFact("", "ville", "Grenoble"),
		UpdateFact("", "animal", "un berger australien nommé Pixel"),
		UpdateFiche("", nasFiche, nasWhen, strings.Join([]string{nasStep1, nasStep2, nasCheck}, "\n")),
		UpdateFiche("", wifiFiche, "Quand la connexion sans fil tombe", "Redémarrer la box puis attendre deux minutes"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	mustOK(t, run(t, "memory_lesson", map[string]any{"text": "Toujours confirmer avant de supprimer un fichier"}))
	mustOK(t, run(t, "memory_lesson", map[string]any{"text": "Donner les horaires au format vingt-quatre heures"}))
	mustOK(t, run(t, "memory_rule", map[string]any{"from": "—", "to": ", "}))
}

func TestGuardRestoresLineLostByRewrite(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	if _, err := BeginSleep(); err == nil {
		t.Error("deux sommeils à la fois devaient être refusés")
	}
	// Le ménage réécrit la fiche entière et en oublie une ligne.
	sleepCall(t, "fiche_save", map[string]any{"name": nasFiche, "when": nasWhen, "content": nasStep1 + "\n" + nasStep2 + " (reformulé)"})
	restored := s.Finish("ok", "Fiche de sauvegarde resserrée")
	if len(restored) != 1 || !strings.Contains(restored[0], "voyant orange") {
		t.Fatalf("restaurations : %v", restored)
	}
	f, _ := GetFiche(nasFiche)
	if !strings.HasSuffix(f.Content, restoredHead+"\n"+nasCheck) || !strings.Contains(f.Content, "(reformulé)") {
		t.Errorf("la ligne perdue devait revenir dans sa section, le reste du ménage rester :\n%s", f.Content)
	}
	if s.Finish("ok", "") != nil {
		t.Error("un sommeil ne se ferme qu'une fois")
	}

	sleeps := Sleeps()
	if len(sleeps) != 1 || sleeps[0].ID != s.id || sleeps[0].Status != "ok" || sleeps[0].Summary != "Fiche de sauvegarde resserrée" {
		t.Fatalf("bilan : %+v", sleeps)
	}
	changes := strings.Join(sleeps[0].Changes, " | ")
	if !strings.Contains(changes, "modifié : fiche "+nasFiche) || !strings.Contains(changes, "restauré : fiche "+nasFiche) {
		t.Errorf("changements : %s", changes)
	}
}

func TestGuardKeepsDeliberateRemovals(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	sleepCall(t, "fiche_patch", map[string]any{"name": nasFiche, "old": "\n" + nasCheck, "new": ""})
	sleepCall(t, "memory_forget", map[string]any{"key": "animal"})
	sleepCall(t, "memory_forget", map[string]any{"key": "lecon-1"})
	sleepCall(t, "memory_forget", map[string]any{"key": "regle-1"})
	sleepCall(t, "memory_remember", map[string]any{"key": "ville", "value": "Annecy"})
	sleepCall(t, "memory_forget", map[string]any{"key": wifiFiche})
	if restored := s.Finish("ok", ""); len(restored) != 0 {
		t.Fatalf("rien ne devait être restauré : %v", restored)
	}
	if f, _ := GetFiche(nasFiche); strings.Contains(f.Content, "voyant") || strings.Contains(f.Content, restoredHead) {
		t.Errorf("ligne retirée exprès remise :\n%s", f.Content)
	}
	if facts, _ := Profile(); len(facts) != 1 || facts[0] != (Fact{"ville", "Annecy"}) {
		t.Errorf("profil : %v", facts)
	}
	if ls := Lessons(); len(ls) != 1 || ls[0].N != 2 {
		t.Errorf("leçons : %v", ls)
	}
	if len(Rules()) != 0 {
		t.Errorf("règles : %v", Rules())
	}
	if f, _ := GetFiche(wifiFiche); !f.Archived {
		t.Error("la fiche oubliée devait rester archivée")
	}
	if changes := strings.Join(Sleeps()[0].Changes, " | "); !strings.Contains(changes, "archivé : fiche "+wifiFiche) {
		t.Errorf("changements : %s", changes)
	}
}

func TestGuardAcceptsMovesAndMerges(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	// La ligne quitte une fiche pour une autre, reformulée au passage.
	sleepCall(t, "fiche_save", map[string]any{"name": wifiFiche, "when": "Quand la connexion sans fil tombe",
		"content": "Redémarrer la box puis attendre deux minutes\nAvant de débrancher le disque, vérifier que le voyant orange est éteint"})
	sleepCall(t, "fiche_save", map[string]any{"name": nasFiche, "when": nasWhen, "content": nasStep1 + "\n" + nasStep2})
	sleepCall(t, "fiche_merge", map[string]any{"from": wifiFiche, "into": nasFiche})
	if restored := s.Finish("ok", ""); len(restored) != 0 {
		t.Fatalf("déplacer et fusionner ne perd rien : %v", restored)
	}
}

func TestGuardRestoresProfileLessonsRulesAndVanishedFiche(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	// Des pertes qui ne passent par aucune opération de retrait : des fichiers
	// réécrits ou supprimés sous les pieds de la mémoire.
	for _, err := range []error{
		home.WriteFile(memPath(profileFile), []byte(profileHead+"- ville: Annecy\n")),
		home.WriteFile(memPath(lessonsFile), []byte(lessonsHead+"- [lecon-2] Donner les horaires au format vingt-quatre heures\n")),
		home.WriteFile(memPath(rulesFile), []byte(rulesHead)),
		os.Remove(fichePath(nasFiche, false)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	restored := strings.Join(s.Finish("interrompu", ""), " | ")
	for _, want := range []string{"profil animal", "leçon « Toujours confirmer", "règle regle-1", "fiche " + nasFiche} {
		if !strings.Contains(restored, want) {
			t.Errorf("%q devait être restauré : %s", want, restored)
		}
	}
	facts, _ := Profile()
	if len(facts) != 2 || facts[0] != (Fact{"ville", "Annecy"}) || facts[1].Key != "animal" {
		t.Errorf("profil (une clé remplacée ne revient pas à l'ancienne valeur) : %v", facts)
	}
	if ls := Lessons(); len(ls) != 2 || ls[1] != (Lesson{1, "Toujours confirmer avant de supprimer un fichier"}) {
		t.Errorf("leçons : %v", ls)
	}
	if rs := Rules(); len(rs) != 1 || rs[0].From != "—" {
		t.Errorf("règles : %v", rs)
	}
	f, err := GetFiche(nasFiche)
	if err != nil || f.When != nasWhen || !strings.Contains(f.Content, nasCheck) || !strings.Contains(f.Content, strings.TrimPrefix(nasStep1, "1. ")) {
		t.Errorf("fiche disparue non recréée : %+v %v", f, err)
	}
}

func TestRestoreSleep(t *testing.T) {
	seedSleepMemory(t)
	before := memoryState()
	if !LastSleep().IsZero() {
		t.Error("aucun sommeil encore")
	}
	s := beginSleep(t)
	if err := RestoreSleep(s.id); err == nil {
		t.Error("pas de restauration pendant un sommeil")
	}
	sleepCall(t, "memory_remember", map[string]any{"key": "ville", "value": "Annecy"})
	sleepCall(t, "fiche_merge", map[string]any{"from": wifiFiche, "into": nasFiche})
	sleepCall(t, "fiche_save", map[string]any{"name": "jardin-arrosage", "when": "Quand il faut arroser le potager", "content": "Arroser tôt le matin, jamais en plein soleil"})
	s.Finish("ok", "Deux fiches fusionnées")
	after := memoryState()
	if after == before {
		t.Fatal("le sommeil n'a rien changé")
	}
	if last := LastSleep(); last.IsZero() || last.Before(LastChange()) {
		t.Errorf("LastSleep (%v) doit suivre la dernière écriture du sommeil (%v)", last, LastChange())
	}

	if err := RestoreSleep(s.id); err != nil {
		t.Fatal(err)
	}
	if got := memoryState(); got != before {
		t.Errorf("mémoire non remise comme avant le sommeil :\n%s\n--- attendu ---\n%s", got, before)
	}
	if f, err := GetFiche(wifiFiche); err != nil || f.Archived {
		t.Errorf("la fiche archivée par le sommeil devait revenir : %+v %v", f, err)
	}
	if _, err := GetFiche("jardin-arrosage"); err == nil {
		t.Error("la fiche créée par le sommeil devait disparaître")
	}

	// Une restauration se défait : l'état d'avant elle a été copié.
	sleeps := Sleeps()
	if len(sleeps) != 2 || sleeps[0].Status != statusRestore || sleeps[1].ID != s.id {
		t.Fatalf("copies : %+v", sleeps)
	}
	if err := RestoreSleep(sleeps[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := memoryState(); got != after {
		t.Errorf("restauration non défaite :\n%s\n--- attendu ---\n%s", got, after)
	}
	if err := RestoreSleep("../ailleurs"); err == nil {
		t.Error("identifiant invalide accepté")
	}
	if err := RestoreSleep("20000101-000000"); err == nil {
		t.Error("copie inconnue acceptée")
	}
}

func TestSleepCopiesArePruned(t *testing.T) {
	setup(t)
	for i := 0; i < sleepsKept+3; i++ {
		if err := UpdateFact("", "tour", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
		s, err := BeginSleep()
		if err != nil {
			t.Fatal(err)
		}
		s.Finish("ok", fmt.Sprintf("sommeil %d", i))
	}
	sleeps := Sleeps()
	if len(sleeps) != sleepsKept || sleeps[0].Summary != fmt.Sprintf("sommeil %d", sleepsKept+2) {
		t.Errorf("%d copies gardées, la plus récente : %q", len(sleeps), sleeps[0].Summary)
	}
}

func TestLastChange(t *testing.T) {
	setup(t)
	if !LastChange().IsZero() {
		t.Error("mémoire vide : aucune modification")
	}
	start := time.Now().Add(-2 * time.Second)
	if err := UpdateFiche("", "essai", "Pour un essai", "contenu"); err != nil {
		t.Fatal(err)
	}
	if got := LastChange(); got.Before(start) {
		t.Errorf("LastChange = %v", got)
	}
	LogExchange("bonjour tout le monde", "bonjour")
	first := LastChange()
	time.Sleep(20 * time.Millisecond)
	LogExchange("le journal ne compte pas", "non")
	if !LastChange().Equal(first) {
		t.Error("une entrée du journal n'est pas une modification de la mémoire")
	}
}

func TestDump(t *testing.T) {
	seedSleepMemory(t)
	run(t, "fiche_read", map[string]any{"name": nasFiche})
	all := Dump(0)
	for _, want := range []string{"## PROFILE\n- ville: Grenoble", "lecon-1: Toujours confirmer", `regle-1: "—" -> ", "`,
		"### fiche " + nasFiche + " (when: " + nasWhen + ")", "opened 1 times, last 0 days ago", "never opened", nasCheck} {
		if !strings.Contains(all, want) {
			t.Errorf("Dump sans %q :\n%s", want, all)
		}
	}

	// Trop de fiches pour tout donner : les groupes qui se ressemblent d'abord.
	body := func(topic string) string {
		var b strings.Builder
		for i := 0; i < 40; i++ {
			fmt.Fprintf(&b, "- %s point %s%d\n", topic, topic, i)
		}
		return b.String()
	}
	for _, name := range []string{"newsletter-envoi", "newsletter-relecture"} {
		if err := UpdateFiche("", name, "Pour la lettre mensuelle : "+name, body("lettre")); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		topic := fmt.Sprintf("theme%dx", i)
		if err := UpdateFiche("", topic, "Sujet isolé "+topic, body(topic)+strings.Repeat(topic+" ", 300)); err != nil {
			t.Fatal(err)
		}
	}
	const limit = 9000
	short := Dump(limit)
	if len(short) > limit {
		t.Errorf("Dump dépasse la limite : %d > %d", len(short), limit)
	}
	alike := strings.Index(short, "## "+RelatedFichesHeading)
	group := strings.Index(short, "### GROUP 1: ")
	others := strings.Index(short, "## OTHER FICHES")
	if alike < 0 || group < alike || others < group {
		t.Fatalf("sections attendues (groupes, puis le reste en liste) :\n%s", short)
	}
	if !strings.Contains(short[group:others], "#### fiche newsletter-envoi") || !strings.Contains(short[group:others], "#### fiche newsletter-relecture") {
		t.Errorf("le groupe des deux fiches proches devait être donné en entier")
	}
	if n := strings.Count(short[others:], "- theme"); n == 0 || !strings.Contains(short[others:], " chars\n") {
		t.Errorf("le reste devait être en liste (%d lignes)", n)
	}
	for _, name := range ficheNamesForTest() {
		if !strings.Contains(short, name+" (when: ") {
			t.Errorf("fiche %s absente du Dump borné", name)
		}
	}
}

func ficheNamesForTest() []string {
	var names []string
	for _, f := range Fiches() {
		names = append(names, f.Name)
	}
	return names
}

// memoryState : tous les fichiers de la mémoire, dans un ordre fixe.
func memoryState() string {
	mu.Lock()
	defer mu.Unlock()
	var files []string
	snap, err := snapshotLocked()
	if err != nil {
		return "illisible : " + err.Error()
	}
	for rel, content := range snap {
		files = append(files, "=== "+rel+"\n"+content)
	}
	sort.Strings(files)
	return strings.Join(files, "\n")
}

// Pendant un sommeil, l'utilisateur et les conversations continuent de retirer
// des choses : le garde-fou ne doit pas les remettre.
func TestGuardKeepsRemovalsMadeOutsideTheSleep(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	for _, err := range []error{
		ForgetFact("animal"),
		ForgetLesson(1),
		ForgetRule(1),
		DeleteFiche(nasFiche),
		// L'utilisateur réécrit une fiche dans l'interface et en ôte une phrase.
		UpdateFiche(wifiFiche, wifiFiche, "Quand la connexion sans fil tombe", "Appeler le fournisseur"),
		// Il renomme une ligne du profil.
		UpdateFact("ville", "domicile", "Grenoble"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if restored := s.Finish("ok", ""); len(restored) != 0 {
		t.Fatalf("des retraits voulus ont été annulés : %v", restored)
	}
	if facts, _ := Profile(); len(facts) != 1 || facts[0] != (Fact{"domicile", "Grenoble"}) {
		t.Errorf("profil : %v", facts)
	}
	if ls := Lessons(); len(ls) != 1 || ls[0].N != 2 {
		t.Errorf("leçons : %v", ls)
	}
	if len(Rules()) != 0 {
		t.Errorf("règles : %v", Rules())
	}
	if _, err := GetFiche(nasFiche); err == nil {
		t.Error("la fiche supprimée par l'utilisateur est revenue")
	}
	if f, _ := GetFiche(wifiFiche); f.Content != "Appeler le fournisseur" {
		t.Errorf("la fiche réécrite par l'utilisateur a été complétée :\n%s", f.Content)
	}
}

// Une fiche renommée dans l'interface pendant un sommeil ne revient pas sous
// son ancien nom.
func TestGuardFollowsRenamedFiche(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	if err := UpdateFiche(wifiFiche, "box-internet", "Quand la box ne répond plus", "Débrancher la prise dix secondes"); err != nil {
		t.Fatal(err)
	}
	if restored := s.Finish("ok", ""); len(restored) != 0 {
		t.Fatalf("rien à restaurer : %v", restored)
	}
	if _, err := GetFiche(wifiFiche); err == nil {
		t.Error("l'ancien nom est revenu")
	}
}

// Un fichier illisible n'est pas un fichier vide : ni sommeil ni restauration
// ne partent d'une lecture ratée.
func TestSleepRefusesUnreadableMemory(t *testing.T) {
	seedSleepMemory(t)
	unreadable(t, memPath(profileFile))
	if s, err := BeginSleep(); err == nil {
		s.Finish("erreur", "")
		t.Fatal("un sommeil a commencé sur une mémoire illisible")
	}
}

// Si la mémoire devient illisible pendant le sommeil, le garde-fou ne
// « restaure » pas par-dessus ce qu'il n'a pas pu lire.
func TestGuardSkippedWhenMemoryUnreadable(t *testing.T) {
	seedSleepMemory(t)
	s := beginSleep(t)
	restore := unreadable(t, memPath(lessonsFile))
	if restored := s.Finish("ok", ""); len(restored) != 0 {
		t.Errorf("restauration à l'aveugle : %v", restored)
	}
	if changes := strings.Join(Sleeps()[0].Changes, " | "); !strings.Contains(changes, "illisible") {
		t.Errorf("le bilan devait le dire : %s", changes)
	}
	restore()
	if ls := Lessons(); len(ls) != 2 {
		t.Errorf("leçons : %v", ls)
	}
}
