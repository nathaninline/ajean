package ajean

import (
	"testing"
	"time"
)

func TestNextAfterInterval(t *testing.T) {
	from := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	got, err := nextAfter("@every 2h", "", from)
	if err != nil {
		t.Fatalf("erreur inattendue : %v", err)
	}
	want := from.Add(2 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("@every 2h : got %v, want %v", got, want)
	}
	// Casse tolérée sur le préfixe.
	if _, err := nextAfter("@EVERY 30m", "", from); err != nil {
		t.Fatalf("@EVERY 30m devrait être valide : %v", err)
	}
	// Sous la minute → refusé.
	if _, err := nextAfter("@every 10s", "", from); err == nil {
		t.Fatalf("@every 10s aurait dû être refusé")
	}
	if _, err := nextAfter("@every pouet", "", from); err == nil {
		t.Fatalf("durée invalide aurait dû être refusée")
	}
}

func TestNextEveryDays(t *testing.T) {
	from := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	// Sans ancre horaire : intervalle glissant de N*24h.
	got, err := nextAfter("@every 3d", "", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	if want := from.Add(72 * time.Hour); !got.Equal(want) {
		t.Fatalf("@every 3d : got %v, want %v", got, want)
	}
}

func TestNextEveryDayAtTime(t *testing.T) {
	// « tous les 1 jour à 23:00 », fuseau Europe/Paris (CEST = UTC+2 en août).
	from := time.Date(2026, 8, 15, 20, 0, 0, 0, time.UTC) // 22h à Paris
	got, err := nextAfter("@every 1d@23:00", "Europe/Paris", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	// 23:00 Paris le 15 = 21:00 UTC.
	want := time.Date(2026, 8, 15, 21, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("@every 1d@23:00 : got %v, want %v", got.UTC(), want)
	}
	// Après 23:00 Paris → le lendemain.
	from2 := time.Date(2026, 8, 15, 21, 30, 0, 0, time.UTC) // 23h30 Paris
	got2, _ := nextAfter("@every 1d@23:00", "Europe/Paris", from2)
	want2 := time.Date(2026, 8, 16, 21, 0, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Fatalf("lendemain : got %v, want %v", got2.UTC(), want2)
	}
}

func TestNextCronEveryQuarter(t *testing.T) {
	from := time.Date(2026, 8, 15, 10, 7, 30, 0, time.UTC)
	got, err := nextAfter("*/15 * * * *", "", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	want := time.Date(2026, 8, 15, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("*/15 : got %v, want %v", got, want)
	}
}

func TestNextCronWeekdayMorning(t *testing.T) {
	// 2026-08-15 est un samedi. « 0 9 * * 1-5 » → lundi 17 août 09:00.
	from := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	got, err := nextAfter("0 9 * * 1-5", "UTC", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	want := time.Date(2026, 8, 17, 9, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("0 9 * * 1-5 : got %v, want %v", got, want)
	}
}

func TestNextCronInParisTZ(t *testing.T) {
	// « 0 9 * * * » à Paris = 07:00 UTC en été.
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	got, err := nextAfter("0 9 * * *", "Europe/Paris", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	want := time.Date(2026, 8, 15, 7, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("cron TZ : got %v, want %v", got.UTC(), want)
	}
}

func TestNextCronSundayBothForms(t *testing.T) {
	from := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC) // samedi
	got, err := nextAfter("0 0 * * 7", "UTC", from)
	if err != nil {
		t.Fatalf("erreur : %v", err)
	}
	want := time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("dow=7 : got %v, want %v", got, want)
	}
}

func TestParseCronInvalid(t *testing.T) {
	cases := []string{
		"",
		"* * * *",         // 4 champs
		"* * * * * *",     // 6 champs
		"60 * * * *",      // minute hors bornes
		"* 24 * * *",      // heure hors bornes
		"5-1 * * * *",     // plage inversée
		"*/0 * * * *",     // pas nul
		"abc * * * *",     // non numérique
		"@every",          // durée absente
		"@every 1d@25:00", // heure hors bornes
		"@every 0d@10:00", // zéro jour
	}
	for _, c := range cases {
		if err := validateSchedule(c, ""); err == nil {
			t.Errorf("schedule %q aurait dû être refusé", c)
		}
	}
}

func TestValidateScheduleOK(t *testing.T) {
	cases := []string{"@every 1m", "@every 2h", "@every 3d", "@every 1d@23:00", "0 */2 * * *", "30 8 * * 1-5", "0 0 1 * *"}
	for _, c := range cases {
		if err := validateSchedule(c, "Europe/Paris"); err != nil {
			t.Errorf("schedule %q aurait dû être valide : %v", c, err)
		}
	}
}
