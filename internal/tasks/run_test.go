package tasks

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fail : une tâche du modèle qui échoue toujours.
func fail(context.Context, Task) (string, error) { return "", errors.New("API injoignable") }

// say : une tâche du modèle qui rend toujours ce compte rendu.
func say(report string) func(context.Context, Task) (string, error) {
	return func(context.Context, Task) (string, error) { return report, nil }
}

func TestOneShotRunsThenIsDisabled(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Name: ptr("Linge"), Prompt: ptr("Rappeler de sortir le linge"), InMinutes: 10, Conv: ptr("c7")}, now())
	r.run = say("  Pense à sortir le linge.\n")
	_, before := r.seen()

	if tick(now().Add(9 * time.Minute)) {
		t.Fatal("exécutée avant l'échéance")
	}
	due := now().Add(10 * time.Minute)
	if !tick(due) {
		t.Fatal("non exécutée à l'échéance")
	}
	v := get(t, task.ID)
	if v.Enabled || !v.Next.IsZero() || v.Running || len(v.Runs) != 1 {
		t.Fatalf("après exécution : %+v", v.Task)
	}
	if run := v.Runs[0]; run.Status != statusOK || run.Report != "Pense à sortir le linge." || !run.At.Equal(due) || run.Seconds < 0 {
		t.Errorf("exécution notée : %+v", run)
	}
	delivered, after := r.seen()
	if len(delivered) != 1 || delivered[0] != "Linge: Pense à sortir le linge." || r.convs[0] != "c7" {
		t.Errorf("livré : %q dans %q", delivered, r.convs)
	}
	if after != before+2 {
		t.Errorf("%d changements signalés pendant l'exécution, 2 attendus (début et fin)", after-before)
	}
	if tick(due.Add(time.Hour)) {
		t.Error("une tâche ponctuelle a été exécutée deux fois")
	}
}

func TestRecurringIsRescheduledWithoutCatchUp(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Name: ptr("Matin"), Prompt: ptr("p"), Unit: "day", Time: "09:00"}, now())
	if !task.Next.Equal(local(2026, 11, 16, 9, 0)) {
		t.Fatalf("première échéance : %v", task.Next)
	}
	// La machine est restée éteinte quatre jours.
	back := local(2026, 11, 20, 10, 0)
	var during time.Time
	r.run = func(context.Context, Task) (string, error) {
		v := get(t, task.ID)
		if !v.Running {
			t.Error("la tâche en cours n'est pas montrée comme telle")
		}
		during = v.Next
		return "bonjour", nil
	}
	if !tick(back) {
		t.Fatal("non exécutée au retour")
	}
	want := local(2026, 11, 21, 9, 0)
	if !during.Equal(want) {
		t.Errorf("pendant l'exécution, prochaine échéance = %v : la tâche doit être reprogrammée avant de partir", during)
	}
	if v := get(t, task.ID); !v.Next.Equal(want) || !v.Enabled || len(v.Runs) != 1 {
		t.Errorf("après exécution : %+v", v.Task)
	}
	if tick(back) || tick(back.Add(22*time.Hour)) {
		t.Error("une occurrence manquée a été rattrapée")
	}
	if !tick(want) {
		t.Error("non exécutée à l'échéance suivante")
	}
	if delivered, _ := r.seen(); len(delivered) != 2 {
		t.Errorf("livré : %q", delivered)
	}
}

func TestRunsKeepTheLastTen(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Prompt: ptr("p"), Every: 1, Unit: "hour"}, now())
	r.run = say(strings.Repeat("é", maxReport+500))
	for i := 1; i <= maxRuns+2; i++ {
		if !tick(now().Add(time.Duration(i) * time.Hour)) {
			t.Fatalf("tour %d : rien d'exécuté", i)
		}
	}
	v := get(t, task.ID)
	if len(v.Runs) != maxRuns || !v.Runs[0].At.Equal(now().Add(12*time.Hour)) || !v.Runs[9].At.Equal(now().Add(3*time.Hour)) {
		t.Errorf("%d exécutions gardées, de %v à %v", len(v.Runs), v.Runs[0].At, v.Runs[len(v.Runs)-1].At)
	}
	if n := len([]rune(v.Runs[0].Report)); n > maxReport+100 || !strings.Contains(v.Runs[0].Report, "tronqué") {
		t.Errorf("compte rendu gardé : %d caractères", n)
	}
	if delivered, _ := r.seen(); len([]rune(delivered[0])) < maxReport+500 {
		t.Error("le compte rendu livré a été tronqué")
	}
}

func TestToolTask(t *testing.T) {
	r := setup(t)
	addScript(t, "prix", `function run(args) { console.log("vol " + args.flight); return args.price < 300 ? "Le vol est à " + args.price + " €" : "" }`)
	addScript(t, "casse", `function run() { throw new Error("site injoignable") }`)

	cheap := mustSave(t, Input{Name: ptr("Vol"), Tool: ptr("prix"), Args: map[string]any{"flight": "AF123", "price": 250.0}, Every: 1, Unit: "minute", Conv: ptr("c1")}, now())
	if !tick(now().Add(time.Minute)) {
		t.Fatal("tâche d'outil non exécutée")
	}
	if run := get(t, cheap.ID).Runs[0]; run.Status != statusOK || run.Report != "vol AF123\nLe vol est à 250 €" {
		t.Errorf("exécution de l'outil : %+v", run)
	}
	delivered, _ := r.seen()
	if len(delivered) != 1 || delivered[0] != "Vol: vol AF123\nLe vol est à 250 €" {
		t.Errorf("livré : %q", delivered)
	}
	if len(r.ran) != 0 {
		t.Error("une tâche d'outil est passée par le modèle")
	}
	if err := Delete(cheap.ID); err != nil {
		t.Fatal(err)
	}

	// Silencieuse : une sortie vide est notée, pas livrée.
	silent := mustSave(t, Input{Name: ptr("Veille"), Tool: ptr("prix"), Args: map[string]any{"price": 400.0}, Quiet: ptr(true), InMinutes: 1}, now())
	addScript(t, "muet", `function run() { return "" }`)
	mute := mustSave(t, Input{Name: ptr("Muet"), Tool: ptr("muet"), Quiet: ptr(true), InMinutes: 2}, now())
	loud := mustSave(t, Input{Name: ptr("Bavard"), Tool: ptr("muet"), InMinutes: 3}, now())
	broken := mustSave(t, Input{Name: ptr("Cassé"), Tool: ptr("casse"), InMinutes: 4}, now())
	later := now().Add(time.Hour)
	for range 4 {
		if !tick(later) {
			t.Fatal("une tâche d'outil échue n'est pas partie")
		}
	}
	// « prix » écrit une ligne avec console.log : ce n'est pas une sortie vide.
	if run := get(t, silent.ID).Runs[0]; run.Status != statusOK || run.Report != "vol undefined" {
		t.Errorf("outil silencieux qui écrit : %+v", run)
	}
	if run := get(t, mute.ID).Runs[0]; run.Status != statusNothing || run.Report != "" {
		t.Errorf("outil silencieux sans sortie : %+v", run)
	}
	if run := get(t, loud.ID).Runs[0]; run.Status != statusError {
		t.Errorf("outil non silencieux sans sortie : %+v", run)
	}
	if run := get(t, broken.ID).Runs[0]; run.Status != statusError || !strings.Contains(run.Report, "site injoignable") {
		t.Errorf("outil qui plante : %+v", run)
	}
	delivered, _ = r.seen()
	if len(delivered) != 4 || delivered[1] != "Veille: vol undefined" || !strings.HasPrefix(delivered[2], "Bavard: La tâche « Bavard » a échoué") || !strings.Contains(delivered[3], "site injoignable") {
		t.Errorf("livré : %q", delivered)
	}
}

func TestQuietModelTask(t *testing.T) {
	r := setup(t)
	watch := mustSave(t, Input{Name: ptr("Veille"), Prompt: ptr("p"), Quiet: ptr(true), Every: 1, Unit: "hour"}, now())
	plain := mustSave(t, Input{Name: ptr("Simple"), Prompt: ptr("p"), InMinutes: 30}, now())

	r.run = say(" " + Nothing + "\n")
	if first, second := tick(now().Add(time.Hour)), tick(now().Add(time.Hour)); !first || !second {
		t.Fatal("tâches non exécutées")
	}
	if run := get(t, watch.ID).Runs[0]; run.Status != statusNothing || run.Report != "" {
		t.Errorf("tâche silencieuse sans rien à dire : %+v", run)
	}
	// Pour une tâche qui n'est pas silencieuse, NOTHING est un compte rendu comme un autre.
	if run := get(t, plain.ID).Runs[0]; run.Status != statusOK || run.Report != Nothing {
		t.Errorf("tâche ordinaire répondant NOTHING : %+v", run)
	}
	delivered, _ := r.seen()
	if len(delivered) != 1 || delivered[0] != "Simple: NOTHING" {
		t.Errorf("livré : %q", delivered)
	}

	r.run = say("Le vol est passé à 280 €.")
	if !tick(now().Add(2 * time.Hour)) {
		t.Fatal("tâche non exécutée")
	}
	if delivered, _ := r.seen(); len(delivered) != 2 || delivered[1] != "Veille: Le vol est passé à 280 €." {
		t.Errorf("livré : %q", delivered)
	}
	r.run = say("")
	if !tick(now().Add(3*time.Hour)) || get(t, watch.ID).Runs[0].Status != statusNothing {
		t.Error("tâche silencieuse au compte rendu vide : statut « rien » attendu")
	}
}

func TestErrorIsDelivered(t *testing.T) {
	r := setup(t)
	r.run = fail
	task := mustSave(t, Input{Name: ptr("Résumé"), Prompt: ptr("p"), Unit: "day", Time: "09:00"}, now())
	if !tick(local(2026, 11, 16, 9, 0)) {
		t.Fatal("non exécutée")
	}
	v := get(t, task.ID)
	if run := v.Runs[0]; run.Status != statusError || run.Report != "API injoignable" {
		t.Errorf("exécution : %+v", run)
	}
	if !v.Enabled || !v.Next.Equal(local(2026, 11, 17, 9, 0)) {
		t.Errorf("une récurrente en erreur attend sa prochaine échéance : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 1 || delivered[0] != "Résumé: La tâche « Résumé » a échoué : API injoignable" {
		t.Errorf("livré : %q", delivered)
	}
}

func TestQuietRecurringDeliversOnlyThirdErrorInARow(t *testing.T) {
	r := setup(t)
	watch := mustSave(t, Input{Name: ptr("Veille"), Prompt: ptr("p"), Quiet: ptr(true), Every: 1, Unit: "hour"}, now())
	step := func(hours int, run func(context.Context, Task) (string, error), want int) {
		t.Helper()
		r.run = run
		if !tick(now().Add(time.Duration(hours) * time.Hour)) {
			t.Fatalf("heure %d : non exécutée", hours)
		}
		if delivered, _ := r.seen(); len(delivered) != want {
			t.Fatalf("heure %d : %d livraisons, %d attendues (%q)", hours, len(delivered), want, delivered)
		}
	}
	step(1, fail, 0)
	step(2, fail, 0)
	step(3, fail, 1)
	step(4, fail, 1) // la 4ᵉ ne dérange plus
	step(5, say(Nothing), 1)
	step(6, fail, 1) // une réussite remet le compte à zéro
	step(7, fail, 1)
	step(8, fail, 2)
	delivered, _ := r.seen()
	if delivered[0] != "Veille: La tâche « Veille » a échoué 3 fois de suite : API injoignable" {
		t.Errorf("livré : %q", delivered[0])
	}

	// Une tâche silencieuse ponctuelle, elle, signale son erreur tout de suite.
	if err := Delete(watch.ID); err != nil {
		t.Fatal(err)
	}
	mustSave(t, Input{Name: ptr("Une fois"), Prompt: ptr("p"), Quiet: ptr(true), At: "2026-11-16 03:00"}, now())
	if !tick(local(2026, 11, 16, 3, 0)) {
		t.Fatal("non exécutée")
	}
	if delivered, _ := r.seen(); len(delivered) != 3 || !strings.HasPrefix(delivered[2], "Une fois: ") {
		t.Errorf("livré : %q", delivered)
	}
}

func TestOneShotIsRetriedOnce(t *testing.T) {
	r := setup(t)
	r.run = fail
	task := mustSave(t, Input{Name: ptr("Rappel"), Prompt: ptr("p"), InMinutes: 10}, now())
	due := now().Add(10 * time.Minute)
	if !tick(due) {
		t.Fatal("non exécutée")
	}
	v := get(t, task.ID)
	if !v.Enabled || !v.Retried || v.Next.Before(due.Add(retryAfter)) || v.Next.After(due.Add(retryAfter+5*time.Second)) {
		t.Fatalf("après le premier échec : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 1 || !strings.Contains(delivered[0], "a échoué : API injoignable") || !strings.Contains(delivered[0], "Nouvelle tentative dans 5 minutes") {
		t.Errorf("livré : %q", delivered)
	}
	if tick(due.Add(4 * time.Minute)) {
		t.Error("retentée avant les 5 minutes")
	}
	if !tick(due.Add(6 * time.Minute)) {
		t.Fatal("non retentée après 5 minutes")
	}
	v = get(t, task.ID)
	if v.Enabled || !v.Next.IsZero() || v.Retried || len(v.Runs) != 2 || v.Runs[0].Status != statusError {
		t.Errorf("après le second échec : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 2 || strings.Contains(delivered[1], "Nouvelle tentative") {
		t.Errorf("livré : %q", delivered)
	}
	if tick(due.Add(time.Hour)) {
		t.Error("retentée une troisième fois")
	}

	// La seconde chance peut réussir.
	other := mustSave(t, Input{Name: ptr("Autre"), Prompt: ptr("p"), InMinutes: 10}, due.Add(2*time.Hour))
	if !tick(due.Add(3 * time.Hour)) {
		t.Fatal("non exécutée")
	}
	r.run = say("C'est l'heure.")
	if !tick(due.Add(4 * time.Hour)) {
		t.Fatal("non retentée")
	}
	if v := get(t, other.ID); v.Enabled || v.Retried || v.Runs[0].Status != statusOK || v.Runs[1].Status != statusError {
		t.Errorf("seconde chance réussie : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 4 || delivered[3] != "Autre: C'est l'heure." {
		t.Errorf("livré : %q", delivered)
	}
}

// blocking rend une tâche du modèle qui attend la fin de son contexte, et un
// canal fermé quand elle a commencé.
func blocking() (run func(context.Context, Task) (string, error), started chan struct{}) {
	started = make(chan struct{})
	return func(ctx context.Context, _ Task) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}, started
}

// wait attend la fermeture du canal, sans bloquer les tests si elle ne vient pas.
func wait(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s : toujours rien après 5 s", what)
	}
}

func TestStop(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Name: ptr("Longue"), Prompt: ptr("p"), InMinutes: 10}, now())
	run, started := blocking()
	r.run = run
	done := make(chan struct{})
	go func() {
		tick(now().Add(time.Hour))
		close(done)
	}()
	wait(t, started, "début de l'exécution")
	if !get(t, task.ID).Running {
		t.Error("la tâche en cours n'est pas montrée comme telle")
	}
	Stop("t00000000") // une autre tâche : sans effet
	select {
	case <-done:
		t.Fatal("arrêter une autre tâche a arrêté celle-ci")
	case <-time.After(50 * time.Millisecond):
	}
	Stop(task.ID)
	wait(t, done, "fin de l'exécution")

	v := get(t, task.ID)
	if v.Running || v.Enabled || !v.Next.IsZero() || v.Runs[0].Status != statusStopped {
		t.Errorf("après l'arrêt : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 0 {
		t.Errorf("une tâche arrêtée a livré : %q", delivered)
	}
}

func TestDeleteStopsRunningTask(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Prompt: ptr("p"), Every: 1, Unit: "hour"}, now())
	run, started := blocking()
	r.run = run
	done := make(chan struct{})
	go func() {
		tick(now().Add(time.Hour))
		close(done)
	}()
	wait(t, started, "début de l'exécution")
	if err := Delete(task.ID); err != nil {
		t.Fatal(err)
	}
	wait(t, done, "fin de l'exécution")
	if delivered, _ := r.seen(); len(List()) != 0 || len(delivered) != 0 {
		t.Errorf("après suppression en cours d'exécution : %d tâches, livré %q", len(List()), delivered)
	}
}

// L'arrêt du programme coupe la tâche en cours : une ponctuelle reste due et
// repartira au prochain démarrage.
func TestShutdownKeepsOneShotDue(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Prompt: ptr("p"), InMinutes: 10}, now())
	run, started := blocking()
	r.run = run
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runDue(ctx, now().Add(time.Hour))
		close(done)
	}()
	wait(t, started, "début de l'exécution")
	cancel()
	wait(t, done, "fin de l'exécution")
	v := get(t, task.ID)
	if !v.Enabled || !v.Next.Equal(task.Next) || v.Retried || v.Runs[0].Status != statusStopped {
		t.Errorf("après l'arrêt du programme : %+v", v.Task)
	}
	if delivered, _ := r.seen(); len(delivered) != 0 {
		t.Errorf("livré : %q", delivered)
	}
}

func TestRunIsBoundedInTime(t *testing.T) {
	r := setup(t)
	old := maxRun
	maxRun = 50 * time.Millisecond
	t.Cleanup(func() { maxRun = old })

	task := mustSave(t, Input{Name: ptr("Lente"), Prompt: ptr("p"), Every: 1, Unit: "hour"}, now())
	r.run, _ = blocking()
	if !tick(now().Add(time.Hour)) {
		t.Fatal("non exécutée")
	}
	if run := get(t, task.ID).Runs[0]; run.Status != statusError || !strings.Contains(run.Report, "durée maximale dépassée") {
		t.Errorf("exécution trop longue : %+v", run)
	}
	if delivered, _ := r.seen(); len(delivered) != 1 {
		t.Errorf("livré : %q", delivered)
	}
}

func TestPause(t *testing.T) {
	r := setup(t)
	daily := mustSave(t, Input{Name: ptr("Quotidienne"), Prompt: ptr("p"), Unit: "day", Time: "09:00"}, now())
	once := mustSave(t, Input{Name: ptr("Ponctuelle"), Prompt: ptr("p"), InMinutes: 10}, now())
	if Paused() {
		t.Fatal("en pause dès le départ")
	}
	_, before := r.seen()
	if err := SetPaused(true); err != nil {
		t.Fatal(err)
	}
	if _, after := r.seen(); !Paused() || after != before+1 {
		t.Errorf("pause : Paused() = %v, %d changements signalés", Paused(), after-before)
	}
	later := local(2026, 11, 18, 12, 0)
	if tick(later) {
		t.Fatal("une tâche est partie pendant la pause")
	}
	if err := RunNow(daily.ID); err == nil {
		t.Error("RunNow pendant la pause : erreur attendue")
	}
	if v := get(t, daily.ID); !v.Next.Equal(daily.Next) {
		t.Errorf("la pause a touché au calendrier : %v", v.Next)
	}

	// À la reprise, pas de rafale : la récurrente attend sa prochaine échéance,
	// la ponctuelle en retard part.
	if err := setPaused(false, later); err != nil {
		t.Fatal(err)
	}
	if v := get(t, daily.ID); Paused() || !v.Next.Equal(local(2026, 11, 19, 9, 0)) {
		t.Errorf("reprise : Paused() = %v, prochaine = %v", Paused(), v.Next)
	}
	if !tick(later) || tick(later) {
		t.Error("à la reprise, seule la tâche ponctuelle en retard doit partir")
	}
	if delivered, _ := r.seen(); len(delivered) != 1 || !strings.HasPrefix(delivered[0], "Ponctuelle: ") || get(t, once.ID).Enabled {
		t.Errorf("livré : %q", delivered)
	}
}

func TestRunNow(t *testing.T) {
	r := setup(t)
	daily := mustSave(t, Input{Name: ptr("Quotidienne"), Prompt: ptr("p"), Unit: "day", Time: "09:00"}, now())
	once := mustSave(t, Input{Name: ptr("Ponctuelle"), Prompt: ptr("p"), InMinutes: 30}, now())
	off := mustSave(t, Input{Name: ptr("Suspendue"), Prompt: ptr("p"), InMinutes: 30, Enabled: ptr(false)}, now())

	if err := RunNow("t00000000"); err == nil {
		t.Error("RunNow d'une tâche inconnue : erreur attendue")
	}
	for _, id := range []string{daily.ID, once.ID, off.ID} {
		if err := RunNow(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunNow(once.ID); err == nil {
		t.Error("RunNow deux fois : erreur attendue")
	}
	Stop(off.ID) // annule un lancement qui n'a pas commencé
	if first, second, third := tick(now()), tick(now()), tick(now()); !first || !second || third {
		t.Fatal("deux tâches devaient partir, dans l'ordre des demandes")
	}
	if got := strings.Join(r.ran, ","); got != "Quotidienne,Ponctuelle" {
		t.Errorf("exécutées : %s", got)
	}
	// Le calendrier n'a pas bougé.
	if v := get(t, daily.ID); !v.Next.Equal(daily.Next) || len(v.Runs) != 1 {
		t.Errorf("récurrente après RunNow : %+v", v.Task)
	}
	if v := get(t, once.ID); !v.Enabled || !v.Next.Equal(once.Next) || len(v.Runs) != 1 {
		t.Errorf("ponctuelle après RunNow : %+v", v.Task)
	}
	if len(get(t, off.ID).Runs) != 0 {
		t.Error("un lancement annulé a eu lieu")
	}
	// Une tâche suspendue peut être lancée à la main, et le reste.
	if err := RunNow(off.ID); err != nil || !tick(now()) {
		t.Fatalf("RunNow d'une tâche suspendue : %v", err)
	}
	if v := get(t, off.ID); v.Enabled || len(v.Runs) != 1 {
		t.Errorf("suspendue après RunNow : %+v", v.Task)
	}
	// Un échec à la demande ne programme pas de seconde chance.
	r.run = fail
	if err := RunNow(once.ID); err != nil || !tick(now()) {
		t.Fatalf("RunNow : %v", err)
	}
	if v := get(t, once.ID); v.Retried || !v.Next.Equal(once.Next) || v.Runs[0].Status != statusError {
		t.Errorf("ponctuelle après un RunNow en échec : %+v", v.Task)
	}
}

func TestDueTasksRunOneByOneInOrder(t *testing.T) {
	r := setup(t)
	mustSave(t, Input{Name: ptr("troisième"), Prompt: ptr("p"), InMinutes: 30}, now())
	mustSave(t, Input{Name: ptr("première"), Prompt: ptr("p"), InMinutes: 10}, now())
	mustSave(t, Input{Name: ptr("deuxième"), Prompt: ptr("p"), Every: 20, Unit: "minute"}, now())
	mustSave(t, Input{Name: ptr("pas encore"), Prompt: ptr("p"), InMinutes: 90}, now())
	later := now().Add(time.Hour)
	for i := range 3 {
		if !tick(later) {
			t.Fatalf("tour %d : rien d'exécuté", i)
		}
		if len(r.ran) != i+1 {
			t.Fatalf("tour %d : %d tâches exécutées, une seule par tour attendue", i, len(r.ran))
		}
	}
	if tick(later) {
		t.Error("une tâche non échue est partie")
	}
	if got := strings.Join(r.ran, ","); got != "première,deuxième,troisième" {
		t.Errorf("ordre d'exécution : %s", got)
	}
}

// L'échéancier réel : les tâches échues partent sans attendre, jamais deux à
// la fois, et RunNow le réveille.
func TestStart(t *testing.T) {
	r := setup(t)
	past := time.Now().Add(-time.Hour)
	mustSave(t, Input{Name: ptr("b"), Prompt: ptr("p"), InMinutes: 20}, past)
	mustSave(t, Input{Name: ptr("a"), Prompt: ptr("p"), InMinutes: 10}, past)
	idle := mustSave(t, Input{Name: ptr("c"), Prompt: ptr("p"), Unit: "month"}, time.Now())

	var active, overlap atomic.Int32
	ran := make(chan string, 10)
	h := r.hooks()
	h.Run = func(_ context.Context, task Task) (string, error) {
		if active.Add(1) > 1 {
			overlap.Add(1)
		}
		time.Sleep(30 * time.Millisecond)
		active.Add(-1)
		ran <- task.Name
		return "fait", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		Start(ctx, h)
		close(stopped)
	}()
	next := func() string {
		t.Helper()
		select {
		case name := <-ran:
			return name
		case <-time.After(5 * time.Second):
			t.Fatal("l'échéancier n'a rien lancé en 5 s")
			return ""
		}
	}
	if first, second := next(), next(); first != "a" || second != "b" {
		t.Errorf("ordre : %s puis %s", first, second)
	}
	if err := RunNow(idle.ID); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != "c" {
		t.Errorf("RunNow a lancé %s", got)
	}
	if overlap.Load() != 0 {
		t.Error("deux tâches ont tourné en même temps")
	}
	cancel()
	wait(t, stopped, "arrêt de l'échéancier")
}

func TestHookPanicsBecomeErrors(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Name: ptr("Fragile"), Prompt: ptr("p"), Every: 1, Unit: "hour"}, now())
	r.run = func(context.Context, Task) (string, error) { panic("index hors limites") }
	if !tick(now().Add(time.Hour)) {
		t.Fatal("non exécutée")
	}
	if run := get(t, task.ID).Runs[0]; run.Status != statusError || !strings.Contains(run.Report, "index hors limites") {
		t.Errorf("panique de Run : %+v", run)
	}
	if delivered, _ := r.seen(); len(delivered) != 1 || !strings.Contains(delivered[0], "a échoué") {
		t.Errorf("livré : %q", delivered)
	}
	if get(t, task.ID).Running {
		t.Error("la tâche est restée « en cours » après la panique")
	}

	// Une livraison qui panique : l'exécution devient une erreur, l'échéancier survit.
	h := r.hooks()
	h.Deliver = func(Task, string) { panic("conversation introuvable") }
	h.Changed = func() { panic("interface fermée") }
	hooks.Store(&h)
	r.run = say("tout va bien")
	if !tick(now().Add(2 * time.Hour)) {
		t.Fatal("non exécutée")
	}
	if run := get(t, task.ID).Runs[0]; run.Status != statusError || !strings.Contains(run.Report, "conversation introuvable") {
		t.Errorf("panique de Deliver : %+v", run)
	}
	if _, err := save(Input{ID: task.ID, Name: ptr("Solide")}, now()); err != nil {
		t.Errorf("panique de Changed : %v", err)
	}
}

// Sans modèle branché, une tâche du modèle échoue proprement.
func TestNoRunHook(t *testing.T) {
	setup(t)
	hooks.Store(&Hooks{})
	task := mustSave(t, Input{Prompt: ptr("p"), Every: 1, Unit: "hour"}, now())
	if !tick(now().Add(time.Hour)) {
		t.Fatal("non exécutée")
	}
	if run := get(t, task.ID).Runs[0]; run.Status != statusError {
		t.Errorf("sans crochet Run : %+v", run)
	}
}

// Pendant son exécution, une tâche peut toucher aux tâches : en créer une,
// changer son propre calendrier.
func TestTaskChangesTasksWhileRunning(t *testing.T) {
	r := setup(t)
	task := mustSave(t, Input{Name: ptr("Mère"), Prompt: ptr("p"), InMinutes: 10}, now())
	later := now().Add(time.Hour)
	r.run = func(context.Context, Task) (string, error) {
		mustSave(t, Input{Name: ptr("Fille"), Prompt: ptr("p"), InMinutes: 30}, later)
		mustSave(t, Input{ID: task.ID, InMinutes: 120}, later)
		return "fait", nil
	}
	if !tick(later) {
		t.Fatal("non exécutée")
	}
	if len(List()) != 2 {
		t.Fatalf("%d tâches, 2 attendues", len(List()))
	}
	// Le nouveau calendrier, posé pendant l'exécution, n'est pas écrasé.
	if v := get(t, task.ID); !v.Enabled || !v.Next.Equal(later.Add(2*time.Hour)) || len(v.Runs) != 1 {
		t.Errorf("tâche reprogrammée par elle-même : %+v", v.Task)
	}
}
