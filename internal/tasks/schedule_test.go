package tasks

import (
	"testing"
	"time"
)

func TestParseAt(t *testing.T) {
	setup(t)
	cases := map[string]time.Time{
		"18:00":            local(2026, 11, 15, 18, 0),
		"09:15":            local(2026, 11, 16, 9, 15), // déjà passé : demain
		"14:30":            local(2026, 11, 16, 14, 30),
		"9h05":             local(2026, 11, 16, 9, 5),
		"18h":              local(2026, 11, 15, 18, 0),
		" 23:59 ":          local(2026, 11, 15, 23, 59),
		"2026-12-24 20:00": local(2026, 12, 24, 20, 0),
		"2026-12-24T20:00": local(2026, 12, 24, 20, 0),
		"2020-01-01 08:00": local(2020, 1, 1, 8, 0), // le refus du passé se décide plus haut
	}
	for in, want := range cases {
		got, err := parseAt(in, now())
		if err != nil || !got.Equal(want) {
			t.Errorf("parseAt(%q) = %v, %v ; attendu %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "demain", "25:00", "2026-13-01 10:00", "24/12/2026 20:00"} {
		if got, err := parseAt(in, now()); err == nil {
			t.Errorf("parseAt(%q) = %v, erreur attendue", in, got)
		}
	}
}

func TestNext(t *testing.T) {
	setup(t)
	var none time.Time
	weekly := Schedule{Every: 1, Unit: "week", Time: "09:00", Days: []string{"mon", "wed", "fri"}}
	fortnight := Schedule{Every: 2, Unit: "week", Time: "09:00", Days: []string{"mon", "thu"}}
	monthly := Schedule{Every: 1, Unit: "month", Time: "09:00", Day: 31}
	cases := []struct {
		label     string
		s         Schedule
		prev, now time.Time
		want      time.Time
	}{
		{"minutes, création", Schedule{Every: 5, Unit: "minute"}, none, now(), local(2026, 11, 15, 14, 35)},
		{"minutes, à l'échéance", Schedule{Every: 5, Unit: "minute"}, now(), now(), local(2026, 11, 15, 14, 35)},
		{"minutes, en retard : le rythme est gardé", Schedule{Every: 5, Unit: "minute"}, local(2026, 11, 15, 14, 2), now(), local(2026, 11, 15, 14, 32)},
		{"minutes, pas encore échue", Schedule{Every: 5, Unit: "minute"}, local(2026, 11, 15, 14, 32), now(), local(2026, 11, 15, 14, 32)},
		{"heures, création", Schedule{Every: 2, Unit: "hour"}, none, now(), local(2026, 11, 15, 16, 30)},
		{"heures, en retard", Schedule{Every: 2, Unit: "hour"}, local(2026, 11, 15, 8, 0), now(), local(2026, 11, 15, 16, 0)},

		{"jour, heure passée : demain", Schedule{Every: 1, Unit: "day", Time: "09:00"}, none, now(), local(2026, 11, 16, 9, 0)},
		{"jour, heure à venir : aujourd'hui", Schedule{Every: 1, Unit: "day", Time: "18:00"}, none, now(), local(2026, 11, 15, 18, 0)},
		{"jour, à l'instant même : demain", Schedule{Every: 1, Unit: "day", Time: "14:30"}, none, now(), local(2026, 11, 16, 14, 30)},
		{"jour, suivant", Schedule{Every: 1, Unit: "day", Time: "09:00"}, local(2026, 11, 15, 9, 0), now(), local(2026, 11, 16, 9, 0)},
		{"jour, plusieurs jours de retard", Schedule{Every: 1, Unit: "day", Time: "09:00"}, local(2026, 11, 10, 9, 0), now(), local(2026, 11, 16, 9, 0)},
		{"tous les 3 jours, création", Schedule{Every: 3, Unit: "day", Time: "09:00"}, none, now(), local(2026, 11, 16, 9, 0)},
		{"tous les 3 jours, en retard", Schedule{Every: 3, Unit: "day", Time: "09:00"}, local(2026, 11, 10, 9, 0), now(), local(2026, 11, 16, 9, 0)},
		{"tous les 3 jours, suivant", Schedule{Every: 3, Unit: "day", Time: "09:00"}, local(2026, 11, 16, 9, 0), local(2026, 11, 16, 9, 0), local(2026, 11, 19, 9, 0)},

		{"semaine, création un dimanche", weekly, none, now(), local(2026, 11, 16, 9, 0)},
		{"semaine, du lundi au mercredi", weekly, local(2026, 11, 16, 9, 0), local(2026, 11, 16, 9, 0), local(2026, 11, 18, 9, 0)},
		{"semaine, du vendredi au lundi", weekly, local(2026, 11, 20, 9, 0), local(2026, 11, 20, 9, 5), local(2026, 11, 23, 9, 0)},
		{"semaine, création un mercredi après l'heure", weekly, none, local(2026, 11, 18, 9, 1), local(2026, 11, 20, 9, 0)},
		{"semaine, en retard", weekly, local(2026, 11, 16, 9, 0), local(2026, 11, 26, 12, 0), local(2026, 11, 27, 9, 0)},
		{"semaine, sans jour (fichier retouché)", Schedule{Every: 1, Unit: "week", Time: "09:00"}, none, now(), local(2026, 11, 16, 9, 0)},

		{"2 semaines, création", fortnight, none, now(), local(2026, 11, 16, 9, 0)},
		{"2 semaines, même semaine", fortnight, local(2026, 11, 16, 9, 0), local(2026, 11, 16, 9, 0), local(2026, 11, 19, 9, 0)},
		{"2 semaines, saut d'une semaine", fortnight, local(2026, 11, 19, 9, 0), local(2026, 11, 19, 9, 0), local(2026, 11, 30, 9, 0)},
		{"2 semaines, en retard : reste sur ses semaines", fortnight, local(2026, 11, 16, 9, 0), local(2026, 12, 20, 10, 0), local(2026, 12, 28, 9, 0)},
		{"2 semaines, un seul jour", Schedule{Every: 2, Unit: "week", Time: "09:00", Days: []string{"sun"}}, local(2026, 11, 15, 9, 0), now(), local(2026, 11, 29, 9, 0)},

		{"mois, création", monthly, none, local(2027, 1, 15, 12, 0), local(2027, 1, 31, 9, 0)},
		{"mois, le 31 en février", monthly, local(2027, 1, 31, 9, 0), local(2027, 1, 31, 9, 0), local(2027, 2, 28, 9, 0)},
		{"mois, le 31 revient en mars", monthly, local(2027, 2, 28, 9, 0), local(2027, 2, 28, 9, 0), local(2027, 3, 31, 9, 0)},
		{"mois, le 31 en avril", monthly, local(2027, 3, 31, 9, 0), local(2027, 3, 31, 9, 0), local(2027, 4, 30, 9, 0)},
		{"mois, février bissextile", monthly, local(2028, 1, 31, 9, 0), local(2028, 2, 1, 0, 0), local(2028, 2, 29, 9, 0)},
		{"mois, création en février", monthly, none, local(2027, 2, 10, 12, 0), local(2027, 2, 28, 9, 0)},
		{"mois, création le jour même après l'heure", Schedule{Every: 1, Unit: "month", Time: "09:00", Day: 15}, none, now(), local(2026, 12, 15, 9, 0)},
		{"mois, passage d'année", Schedule{Every: 1, Unit: "month", Time: "09:00", Day: 15}, local(2026, 12, 15, 9, 0), local(2026, 12, 20, 0, 0), local(2027, 1, 15, 9, 0)},
		{"2 mois", Schedule{Every: 2, Unit: "month", Time: "09:00", Day: 31}, local(2027, 1, 31, 9, 0), local(2027, 2, 1, 0, 0), local(2027, 3, 31, 9, 0)},
		{"3 mois, en retard", Schedule{Every: 3, Unit: "month", Time: "09:00", Day: 5}, local(2026, 1, 5, 9, 0), now(), local(2027, 1, 5, 9, 0)},
	}
	for _, c := range cases {
		if got := c.s.next(c.prev, c.now); !got.Equal(c.want) {
			t.Errorf("%s : %v, attendu %v", c.label, got, c.want)
		}
	}
}

// Une tâche garde son heure d'horloge au changement d'heure, même relue du
// disque (où les dates reviennent sans fuseau).
func TestNextKeepsWallClockAcrossDST(t *testing.T) {
	setup(t)
	daily := Schedule{Every: 1, Unit: "day", Time: "08:00"}
	spring := daily.next(local(2027, 3, 27, 8, 0).UTC(), local(2027, 3, 27, 12, 0))
	if want := local(2027, 3, 28, 8, 0); !spring.Equal(want) || spring.Hour() != 8 {
		t.Errorf("passage à l'heure d'été : %v, attendu %v", spring, want)
	}
	if gap := spring.Sub(local(2027, 3, 27, 8, 0)); gap != 23*time.Hour {
		t.Errorf("écart au passage à l'heure d'été : %v, attendu 23 h", gap)
	}
	autumn := daily.next(local(2026, 10, 24, 8, 0).UTC(), local(2026, 10, 24, 12, 0))
	if want := local(2026, 10, 25, 8, 0); !autumn.Equal(want) || autumn.Hour() != 8 || autumn.Sub(local(2026, 10, 24, 8, 0)) != 25*time.Hour {
		t.Errorf("passage à l'heure d'hiver : %v, attendu %v", autumn, want)
	}

	weekly := Schedule{Every: 1, Unit: "week", Time: "08:00", Days: []string{"sat"}}
	if got, want := weekly.next(local(2027, 3, 27, 8, 0).UTC(), local(2027, 3, 27, 9, 0)), local(2027, 4, 3, 8, 0); !got.Equal(want) || got.Hour() != 8 {
		t.Errorf("hebdomadaire après le changement d'heure : %v, attendu %v", got, want)
	}
	monthly := Schedule{Every: 1, Unit: "month", Time: "08:00", Day: 15}
	if got, want := monthly.next(local(2027, 3, 15, 8, 0).UTC(), local(2027, 3, 16, 0, 0)), local(2027, 4, 15, 8, 0); !got.Equal(want) || got.Hour() != 8 {
		t.Errorf("mensuelle après le changement d'heure : %v, attendu %v", got, want)
	}

	// Un intervalle en heures est une durée réelle : il ne suit pas l'horloge.
	hourly := Schedule{Every: 2, Unit: "hour"}
	if got, want := hourly.next(local(2027, 3, 28, 1, 0), local(2027, 3, 28, 1, 0)), local(2027, 3, 28, 4, 0); !got.Equal(want) {
		t.Errorf("2 heures par-dessus le changement d'heure : %v, attendu %v", got, want)
	}
}

func TestScheduleText(t *testing.T) {
	setup(t)
	cases := map[string]Schedule{
		"une fois, le 16/11 à 18:00":                                       {At: local(2026, 11, 16, 18, 0)},
		"une fois, le 03/01/2027 à 08:05":                                  {At: local(2027, 1, 3, 8, 5)},
		"toutes les minutes":                                               {Every: 1, Unit: "minute"},
		"toutes les 15 minutes":                                            {Every: 15, Unit: "minute"},
		"toutes les heures":                                                {Every: 1, Unit: "hour"},
		"toutes les 2 heures":                                              {Every: 2, Unit: "hour"},
		"tous les jours à 08:00":                                           {Every: 1, Unit: "day", Time: "08:00"},
		"tous les 3 jours à 08:00":                                         {Every: 3, Unit: "day", Time: "08:00"},
		"tous les lundis à 09:00":                                          {Every: 1, Unit: "week", Time: "09:00", Days: []string{"mon"}},
		"tous les lundis et jeudis à 09:00":                                {Every: 1, Unit: "week", Time: "09:00", Days: []string{"mon", "thu"}},
		"tous les lundis, mercredis et vendredis à 09:00":                  {Every: 1, Unit: "week", Time: "09:00", Days: []string{"mon", "wed", "fri"}},
		"tous les jours à 07:30":                                           {Every: 1, Unit: "week", Time: "07:30", Days: weekdays},
		"toutes les 2 semaines, le lundi à 09:00":                          {Every: 2, Unit: "week", Time: "09:00", Days: []string{"mon"}},
		"toutes les 3 semaines, le samedi et le dimanche à 10:00":          {Every: 3, Unit: "week", Time: "10:00", Days: []string{"sat", "sun"}},
		"toutes les 2 semaines, le lundi, le mercredi et le jeudi à 09:00": {Every: 2, Unit: "week", Time: "09:00", Days: []string{"mon", "wed", "thu"}},
		"tous les mois, le 1er à 09:00":                                    {Every: 1, Unit: "month", Time: "09:00", Day: 1},
		"tous les mois, le 31 à 09:00":                                     {Every: 1, Unit: "month", Time: "09:00", Day: 31},
		"tous les 3 mois, le 15 à 18:45":                                   {Every: 3, Unit: "month", Time: "18:45", Day: 15},
	}
	for want, s := range cases {
		if got := s.text(now()); got != want {
			t.Errorf("text(%+v) = %q, attendu %q", s, got, want)
		}
	}
}
