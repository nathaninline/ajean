package tasks

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ajean/internal/config"
)

const (
	maxEvery        = 1000
	minModelMinutes = 5 // chaque exécution d'une tâche du modèle coûte un appel à l'API
)

// weekdays : les jours dans l'ordre de la semaine, qui commence le lundi.
var weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var dayNames = map[string]string{
	"mon": "lundi", "tue": "mardi", "wed": "mercredi", "thu": "jeudi", "fri": "vendredi", "sat": "samedi", "sun": "dimanche",
}

// frenchDays : les modèles et les utilisateurs écrivent aussi les jours en français.
var frenchDays = map[string]string{
	"lun": "mon", "mar": "tue", "mer": "wed", "jeu": "thu", "ven": "fri", "sam": "sat", "dim": "sun",
}

// weekdayIndex : la place d'un jour dans weekdays.
func weekdayIndex(d time.Weekday) int { return (int(d) + 6) % 7 }

// ---- lire ce qu'on nous donne ------------------------------------------------------

// parseClock lit « HH:MM » (ou « 9h30 », « 9h », que les modèles écrivent aussi).
func parseClock(s string) (hour, minute int, err error) {
	s = strings.Replace(strings.ToLower(strings.TrimSpace(s)), "h", ":", 1)
	if strings.HasSuffix(s, ":") {
		s += "00"
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, errors.New("heure illisible : attendu « HH:MM » (ex. 09:00)")
	}
	return t.Hour(), t.Minute(), nil
}

// parseAt lit « HH:MM » (aujourd'hui, ou demain si l'heure est passée) ou
// « AAAA-MM-JJ HH:MM », dans le fuseau de l'utilisateur.
func parseAt(s string, now time.Time) (time.Time, error) {
	loc := config.Location()
	s = strings.TrimSpace(s)
	if t, err := time.ParseInLocation("2006-01-02 15:04", strings.Replace(s, "T", " ", 1), loc); err == nil {
		return t, nil
	}
	hour, minute, err := parseClock(s)
	if err != nil {
		return time.Time{}, errors.New("'at' illisible : attendu « HH:MM » ou « AAAA-MM-JJ HH:MM »")
	}
	today := now.In(loc)
	at := time.Date(today.Year(), today.Month(), today.Day(), hour, minute, 0, 0, loc)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at, nil
}

func parseUnit(s string) (string, error) {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case "minute", "minutes":
		return "minute", nil
	case "hour", "hours", "hourly":
		return "hour", nil
	case "day", "days", "daily":
		return "day", nil
	case "week", "weeks", "weekly":
		return "week", nil
	case "month", "months", "monthly":
		return "month", nil
	case "":
		return "", errors.New("'unit' manquant : minute, hour, day, week ou month")
	}
	return "", fmt.Errorf("'unit' inconnu (« %s ») : minute, hour, day, week ou month", s)
}

// parseDays rend les jours donnés sous leur forme courte, sans doublon, dans
// l'ordre de la semaine. « monday » et « lundi » sont compris.
func parseDays(days []string) ([]string, error) {
	var out []string
	for _, d := range days {
		d = strings.ToLower(strings.TrimSpace(d))
		short := d
		if len(d) > 3 {
			short = d[:3]
		}
		if fr, ok := frenchDays[short]; ok {
			short = fr
		}
		if !slices.Contains(weekdays, short) {
			return nil, fmt.Errorf("jour inconnu (« %s ») : mon, tue, wed, thu, fri, sat ou sun", d)
		}
		if !slices.Contains(out, short) {
			out = append(out, short)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return slices.Index(weekdays, a) - slices.Index(weekdays, b) })
	return out, nil
}

// completed valide un calendrier récurrent et le rend complet : ce qui n'a pas
// été précisé (heure, jour de la semaine, jour du mois) vient de l'instant de
// création, ce qui ne sert pas à l'unité choisie est écarté.
func (s Schedule) completed(now time.Time) (Schedule, error) {
	unit, err := parseUnit(s.Unit)
	if err != nil {
		return Schedule{}, err
	}
	if s.Every < 1 || s.Every > maxEvery {
		return Schedule{}, fmt.Errorf("'every' : un nombre de 1 à %d", maxEvery)
	}
	out := Schedule{Every: s.Every, Unit: unit}
	if unit == "minute" || unit == "hour" {
		return out, nil
	}
	local := now.In(config.Location())
	out.Time = local.Format("15:04")
	if strings.TrimSpace(s.Time) != "" {
		hour, minute, err := parseClock(s.Time)
		if err != nil {
			return Schedule{}, fmt.Errorf("'time' : %w", err)
		}
		out.Time = fmt.Sprintf("%02d:%02d", hour, minute)
	}
	switch unit {
	case "week":
		if out.Days, err = parseDays(s.Days); err != nil {
			return Schedule{}, err
		}
		if len(out.Days) == 0 {
			out.Days = []string{weekdays[weekdayIndex(local.Weekday())]}
		}
	case "month":
		out.Day = s.Day
		if out.Day == 0 {
			out.Day = local.Day()
		}
		if out.Day < 1 || out.Day > 31 {
			return Schedule{}, errors.New("'day' : un jour du mois, de 1 à 31")
		}
	}
	return out, nil
}

// allowed refuse un intervalle trop court pour la façon de travailler de la tâche.
func (s Schedule) allowed(model bool) error {
	if model && s.Unit == "minute" && s.Every < minModelMinutes {
		return fmt.Errorf("intervalle trop court : au moins %d minutes pour une tâche du modèle (chaque exécution appelle l'API) ; une tâche d'outil peut descendre à 1 minute", minModelMinutes)
	}
	return nil
}

// ---- calculer les échéances --------------------------------------------------------

// start : la première échéance d'une tâche qu'on programme à l'instant now.
func (s Schedule) start(now time.Time) (time.Time, error) {
	if s.Every == 0 {
		if !s.At.After(now) {
			return time.Time{}, fmt.Errorf("ce moment est déjà passé (maintenant : %s) : donne un moment à venir (in_minutes ou at)", config.Stamp(now))
		}
		return s.At, nil
	}
	return s.next(time.Time{}, now), nil
}

// next : la première échéance après now d'un calendrier récurrent, à partir de
// l'échéance précédente prev (zéro : la tâche vient d'être programmée). Les
// occurrences manquées ne sont pas rattrapées, mais le rythme est gardé : une
// tâche « toutes les 2 semaines » reste sur ses semaines.
func (s Schedule) next(prev, now time.Time) time.Time {
	if s.Unit == "minute" || s.Unit == "hour" {
		step := time.Duration(s.step()) * time.Minute
		if s.Unit == "hour" {
			step = time.Duration(s.step()) * time.Hour
		}
		switch {
		case prev.IsZero():
			return now.Add(step)
		case prev.After(now):
			return prev
		}
		return prev.Add((now.Sub(prev)/step + 1) * step)
	}
	// Les jours se comptent dans le fuseau de l'utilisateur, à l'heure d'horloge :
	// un rappel de 8 h reste à 8 h au changement d'heure.
	loc := config.Location()
	if prev.IsZero() {
		return s.firstAfter(now.In(loc))
	}
	t := prev.In(loc)
	for !t.After(now) {
		t = s.after(t)
	}
	return t
}

// step : Every, jamais sous 1 (un fichier retouché à la main ne doit pas faire
// tourner le calcul sans fin).
func (s Schedule) step() int { return max(s.Every, 1) }

// on : le jour donné, à l'heure de la tâche.
func (s Schedule) on(year int, month time.Month, day int) time.Time {
	hour, minute, _ := parseClock(s.Time)
	return time.Date(year, month, day, hour, minute, 0, 0, config.Location())
}

// onMonth : le jour voulu du mois donné, ramené au dernier jour quand le mois
// est plus court (un 31 tombe le 28 en février, puis de nouveau le 31 en mars).
func (s Schedule) onMonth(year int, month time.Month) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	last := first.AddDate(0, 1, -1).Day()
	return s.on(first.Year(), first.Month(), min(max(s.Day, 1), last))
}

// has : la tâche tourne-t-elle ce jour de la semaine ?
func (s Schedule) has(weekday int) bool {
	return len(s.Days) == 0 || slices.Contains(s.Days, weekdays[weekday])
}

// firstAfter : la première occurrence après now, sans tenir compte de Every
// (c'est elle qui fixe le rythme des suivantes).
func (s Schedule) firstAfter(now time.Time) time.Time {
	year, month, day := now.Date()
	for i := 0; ; i++ {
		var t time.Time
		switch s.Unit {
		case "month":
			t = s.onMonth(year, month+time.Month(i))
		case "week":
			if t = s.on(year, month, day+i); !s.has(weekdayIndex(t.Weekday())) {
				continue
			}
		default:
			t = s.on(year, month, day+i)
		}
		if t.After(now) {
			return t
		}
	}
}

// after : l'occurrence qui suit l'échéance prev.
func (s Schedule) after(prev time.Time) time.Time {
	year, month, day := prev.Date()
	switch s.Unit {
	case "month":
		return s.onMonth(year, month+time.Month(s.step()))
	case "week":
		pos := weekdayIndex(prev.Weekday())
		for i := pos + 1; i < 7; i++ { // un autre jour de la même semaine
			if s.has(i) {
				return s.on(year, month, day-pos+i)
			}
		}
		monday := day - pos + 7*s.step()
		for i := range 7 {
			if s.has(i) {
				return s.on(year, month, monday+i)
			}
		}
	}
	return s.on(year, month, day+s.step())
}

// ---- dire le calendrier en français ------------------------------------------------

// text : le calendrier en clair, à l'instant now (l'année d'une tâche ponctuelle
// n'est dite que si ce n'est pas l'année en cours).
func (s Schedule) text(now time.Time) string {
	count := func(one, many string) string {
		if s.Every <= 1 {
			return one
		}
		return fmt.Sprintf(many, s.Every)
	}
	switch s.Unit {
	case "minute":
		return count("toutes les minutes", "toutes les %d minutes")
	case "hour":
		return count("toutes les heures", "toutes les %d heures")
	case "day":
		return count("tous les jours", "tous les %d jours") + " à " + s.Time
	case "week":
		return s.weekText() + " à " + s.Time
	case "month":
		day := fmt.Sprint(s.Day)
		if s.Day == 1 {
			day = "1er"
		}
		return count("tous les mois", "tous les %d mois") + ", le " + day + " à " + s.Time
	}
	loc := config.Location()
	at := s.At.In(loc)
	layout := "02/01 à 15:04"
	if at.Year() != now.In(loc).Year() {
		layout = "02/01/2006 à 15:04"
	}
	return "une fois, le " + at.Format(layout)
}

func (s Schedule) weekText() string {
	names := make([]string, len(s.Days))
	if s.Every <= 1 {
		if len(s.Days) == len(weekdays) {
			return "tous les jours"
		}
		for i, d := range s.Days {
			names[i] = dayNames[d] + "s"
		}
		return "tous les " + enumerate(names)
	}
	for i, d := range s.Days {
		names[i] = "le " + dayNames[d]
	}
	return fmt.Sprintf("toutes les %d semaines, %s", s.Every, enumerate(names))
}

// enumerate : « a, b et c ».
func enumerate(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " et " + items[len(items)-1]
}
