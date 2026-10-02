package ajean

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJeanMemory(t *testing.T) {
	testHome(t)

	// Écrire une clé existante la REMPLACE, l'ancienne valeur part au journal.
	if _, err := JeanRemember("Serveur prod", "srvalice"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanRemember("serveur.prod", "srvalice3"); err != nil {
		t.Fatal(err)
	}
	facts, _ := jeanProfile()
	if len(facts) != 1 || facts[0].Value != "srvalice3" {
		t.Fatalf("profil = %+v, veut une seule ligne à jour", facts)
	}
	ctx := jeanContextMessage().Content.(string)
	if !strings.Contains(ctx, "- serveur.prod: srvalice3") || strings.Contains(ctx, "srvalice\n") {
		t.Fatalf("contexte injecté inattendu :\n%s", ctx)
	}

	// Budget dur : le profil refuse de grossir au-delà.
	long := strings.Repeat("x", jeanValueMaxChars)
	var full bool
	for i := 0; i < 40; i++ {
		if _, err := JeanRemember("k"+string(rune('a'+i%26))+string(rune('a'+i/26)), long); err != nil {
			full = strings.Contains(err.Error(), "profil plein")
			break
		}
	}
	if !full {
		t.Fatal("le profil aurait dû refuser au-delà du budget")
	}

	// Journal : échange consigné puis retrouvé, ancienne valeur du profil aussi.
	jeanLogExchange("Ma sœur Léa fête son anniversaire le 12 mars", "Noté !")
	if got := JeanSearch("anniversaire lea", 0); !strings.Contains(got, "Léa") {
		t.Fatalf("recherche journal : %s", got)
	}
	if got := JeanSearch("srvalice", 0); !strings.Contains(got, "Avant : srvalice") {
		t.Fatalf("ancienne valeur introuvable : %s", got)
	}

	// Oubli : retiré du profil, retrouvable dans le journal.
	if _, err := JeanForget("serveur.prod"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(jeanContextMessage().Content.(string), "serveur.prod") {
		t.Fatal("clé oubliée encore injectée")
	}

	// Le contexte Jean est bien rangé avec le contexte « projet » (1er message user).
	if !isProjectSystem(jeanContextMessage()) {
		t.Fatal("le contexte Jean doit suivre le chemin cache-friendly des contextes projet")
	}
}

func TestJeanConversation(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	// conv est global : repartir d'un fil neutre, sinon une exécution précédente
	// (go test -count) laisse le fil Jean affiché et le message arrive en direct.
	conv.mu.Lock()
	conv.ID, conv.Mode, conv.Messages = "", "", nil
	conv.mu.Unlock()
	// Message posté alors que le fil Jean n'est pas affiché : il attend dans l'archive.
	conv.JeanPost("N'oublie pas d'appeler ta mère.")
	conv.JeanPost("SILENCE") // rien à dire : rien de posté
	if err := conv.OpenJean(); err != nil {
		t.Fatal(err)
	}
	conv.mu.Lock()
	id, mode, n := conv.ID, conv.Mode, len(conv.Messages)
	conv.mu.Unlock()
	if id != jeanConvID || mode != "jean" || n != 1 {
		t.Fatalf("fil Jean = id %q mode %q %d messages", id, mode, n)
	}
	// Fil affiché : le message arrive en direct.
	conv.JeanPost("Il pleut demain, prends un parapluie.")
	conv.mu.Lock()
	n = len(conv.Messages)
	conv.mu.Unlock()
	if n != 2 {
		t.Fatalf("message direct non ajouté (%d)", n)
	}
	// Quitter puis revenir : même fil, rien de perdu.
	conv.NewSession()
	if err := conv.OpenJean(); err != nil {
		t.Fatal(err)
	}
	conv.mu.Lock()
	n = len(conv.Messages)
	conv.mu.Unlock()
	if n != 2 {
		t.Fatalf("fil Jean perdu au retour (%d messages)", n)
	}
}

func TestJeanOnceTask(t *testing.T) {
	testHome(t)
	ensureDefaultProject()
	rememberUserTZ("Europe/Paris") // envoyé par le navigateur avec chaque message
	out := toolTaskCreate(map[string]any{"name": "eau", "prompt": "Rappelle de boire", "in_minutes": float64(2), "_jean": true})
	if !strings.Contains(out, "[ok]") || !strings.Contains(out, "Europe/Paris") {
		t.Fatalf("in_minutes : %s", out)
	}
	tasks := listTasks()
	if len(tasks) != 1 || !isOnce(tasks[0].Schedule) || !tasks[0].Jean || tasks[0].TZ != "Europe/Paris" {
		t.Fatalf("tâche = %+v", tasks)
	}
	if d := time.Until(time.UnixMilli(tasks[0].NextRun)); d < time.Minute || d > 3*time.Minute {
		t.Fatalf("échéance dans %v, veut ~2 min", d)
	}
	if out := toolTaskCreate(map[string]any{"name": "x", "prompt": "y", "schedule": "@once 2020-01-01 10:00"}); !strings.Contains(out, "déjà passé") {
		t.Fatalf("date passée acceptée : %s", out)
	}
}

func TestJeanJournalTimeZone(t *testing.T) {
	// Nouvelle entrée (décalage explicite) et ancienne (heure serveur) : même instant.
	now := time.Date(2026, 10, 1, 13, 30, 0, 0, time.UTC)
	es := parseJeanJournal("## 2026-10-01 15:30 +0200 · a · note\nx\n## " + now.In(time.Local).Format("2006-01-02 15:04") + " · b · note\ny\n")
	if len(es) != 2 || !es[0].When.Equal(now) || !es[1].When.Equal(now) {
		t.Fatalf("heures = %+v", es)
	}
	if got := es[0].When.In(loc("Europe/Paris")).Format("15:04"); got != "15:30" {
		t.Fatalf("affichée %s, veut 15:30 (heure française)", got)
	}
}

func TestJeanDeleteEntry(t *testing.T) {
	testHome(t)
	_, _ = JeanNote("garde-moi")
	time.Sleep(2 * time.Millisecond)
	_, _ = JeanNote("efface-moi")
	all := jeanJournalAll()
	if len(all) != 2 {
		t.Fatalf("%d entrées", len(all))
	}
	if err := jeanDeleteEntry(all[1].ID); err != nil {
		t.Fatal(err)
	}
	all = jeanJournalAll()
	if len(all) != 1 || all[0].Text != "garde-moi" {
		t.Fatalf("après suppression : %+v", all)
	}
}

func TestJeanFiches(t *testing.T) {
	testHome(t)
	if _, err := JeanSave("Déployer AJEAN", "", "x"); err == nil {
		t.Fatal("when vide accepté")
	}
	proc := "1. go build\n2. pscp vers le 127\n3. restart ajean-ui"
	if _, err := JeanSave("Déployer AJEAN", "quand je parle de déploiement", proc); err != nil {
		t.Fatal(err)
	}
	// Même nom = nouvelle version, pas un doublon.
	if out, err := JeanSave("deployer-ajean", "quand je parle de déploiement", proc+"\n4. vérifier le lien"); err != nil || !strings.Contains(out, "mise à jour") {
		t.Fatalf("%s %v", out, err)
	}
	if fs := jeanFiches(false); len(fs) != 1 {
		t.Fatalf("%d fiches", len(fs))
	}
	// Index dans le contexte : nom + quand, sans le contenu.
	ctx := jeanContextMessage().Content.(string)
	if !strings.Contains(ctx, "- deployer-ajean: quand je parle de déploiement") || strings.Contains(ctx, "pscp") {
		t.Fatalf("index :\n%s", ctx)
	}
	if got, _ := JeanRead("deployer-ajean"); !strings.Contains(got, "4. vérifier le lien") {
		t.Fatalf("lecture : %s", got)
	}
	if got := JeanSearch("pscp", 0); !strings.Contains(got, "[fiche] deployer-ajean") {
		t.Fatalf("recherche : %s", got)
	}
	// Une note trop longue est refusée (plus de troncature silencieuse).
	if _, err := JeanNote(strings.Repeat("a", jeanNoteMaxChars+1)); err == nil || !strings.Contains(err.Error(), "jean_save") {
		t.Fatalf("note longue : %v", err)
	}
	if _, err := JeanForget("deployer-ajean"); err != nil {
		t.Fatal(err)
	}
	if len(jeanFiches(false)) != 0 {
		t.Fatal("fiche non supprimée")
	}
}

// La mémoire de Jean est réservée à ses outils : ni bash, ni write/edit directs.
func TestJeanMemoryGuarded(t *testing.T) {
	testHome(t)
	if guardToolOnlyPath(filepath.Join(jeanDir(), "profile.md")) == "" {
		t.Fatal("write/edit dans memory/_jean aurait dû être refusé")
	}
	if guardToolOnlyCommand("cat "+filepath.Join(jeanDir(), "profile.md")) == "" {
		t.Fatal("bash sur memory/_jean aurait dû être refusé")
	}
}

func TestJeanIdleExpired(t *testing.T) {
	now := time.Now()
	ev := func(h float64, k string) LogEvent {
		return LogEvent{TS: now.Add(-time.Duration(h * float64(time.Hour))).UnixMilli(), Delta: map[string]any{k: "x"}}
	}
	if !jeanIdleExpired([]LogEvent{ev(5, "user"), ev(1, "jean_post")}, now) {
		t.Fatal("5 h sans message de l'utilisateur : contexte à vider")
	}
	if jeanIdleExpired([]LogEvent{ev(5, "user"), ev(1, "user")}, now) {
		t.Fatal("message récent : on garde le contexte")
	}
	if jeanIdleExpired(nil, now) {
		t.Fatal("fil vide : rien à vider")
	}
}
