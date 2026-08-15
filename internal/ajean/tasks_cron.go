package ajean

// tasks_cron.go — planification des tâches, résolution à la minute. Deux formes :
//
//   - Intervalle : "@every <durée>" (ex. "@every 2h", "@every 30m"), parsé par
//     time.ParseDuration, borné à ≥ 1 minute.
//   - Cron : 5 champs "min hour dom month dow" avec *, listes a,b, plages a-b et
//     pas */n (ex. "0 9 * * 1-5" = 9h du lundi au vendredi).
//
// Aucune dépendance externe : Go n'a pas de lib cron et on n'en ajoute pas pour
// si peu. nextAfter avance minute par minute jusqu'au prochain match — largement
// assez rapide pour une poignée de tâches évaluées une fois par minute.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// minInterval borne la fréquence : sous la minute, le scheduler (ticker 60 s) ne
// pourrait de toute façon pas suivre, et une tâche qui lance une inférence toutes
// les quelques secondes n'a pas de sens.
const minInterval = time.Minute

// nextAfter renvoie le prochain instant (strictement après `from`) où `schedule`
// se déclenche. Erreur si le schedule est invalide.
func nextAfter(schedule string, from time.Time) (time.Time, error) {
	s := strings.TrimSpace(schedule)
	if s == "" {
		return time.Time{}, fmt.Errorf("fréquence vide")
	}
	if rest, ok := cutPrefix(s, "@every"); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return time.Time{}, fmt.Errorf("intervalle invalide (ex. « 2h », « 30m ») : %w", err)
		}
		if d < minInterval {
			return time.Time{}, fmt.Errorf("intervalle minimum : 1 minute")
		}
		return from.Add(d), nil
	}
	return nextCron(s, from)
}

// validateSchedule vérifie qu'un schedule est acceptable, sans calculer de date.
func validateSchedule(schedule string) error {
	_, err := nextAfter(schedule, time.Now())
	return err
}

// cronSpec = les 5 champs compilés en ensembles de valeurs autorisées.
type cronSpec struct {
	min, hour, dom, month, dow map[int]bool
}

// nextCron parse une expression cron 5 champs et cherche le prochain match à la
// minute, en partant de la minute qui suit `from`. Une borne (~366 j) évite une
// boucle infinie sur une expression jamais satisfiable (ex. « 31 février »).
func nextCron(expr string, from time.Time) (time.Time, error) {
	spec, err := parseCron(expr)
	if err != nil {
		return time.Time{}, err
	}
	// On repart de la minute suivante, secondes/nanos remis à zéro.
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := t.Add(366 * 24 * time.Hour)
	for ; t.Before(limit); t = t.Add(time.Minute) {
		if spec.matches(t) {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("fréquence jamais satisfaite : %q", expr)
}

func (s cronSpec) matches(t time.Time) bool {
	return s.min[t.Minute()] &&
		s.hour[t.Hour()] &&
		s.dom[t.Day()] &&
		s.month[int(t.Month())] &&
		s.dow[int(t.Weekday())]
}

func parseCron(expr string) (cronSpec, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return cronSpec{}, fmt.Errorf("cron : 5 champs attendus « min heure jour mois jour-semaine », reçu %d", len(f))
	}
	var s cronSpec
	var err error
	if s.min, err = parseField(f[0], 0, 59); err != nil {
		return cronSpec{}, fmt.Errorf("champ minute : %w", err)
	}
	if s.hour, err = parseField(f[1], 0, 23); err != nil {
		return cronSpec{}, fmt.Errorf("champ heure : %w", err)
	}
	if s.dom, err = parseField(f[2], 1, 31); err != nil {
		return cronSpec{}, fmt.Errorf("champ jour-du-mois : %w", err)
	}
	if s.month, err = parseField(f[3], 1, 12); err != nil {
		return cronSpec{}, fmt.Errorf("champ mois : %w", err)
	}
	// Jour de la semaine : 0 ET 7 valent dimanche. On normalise 7 → 0.
	if s.dow, err = parseField(f[4], 0, 7); err != nil {
		return cronSpec{}, fmt.Errorf("champ jour-semaine : %w", err)
	}
	if s.dow[7] {
		s.dow[0] = true
	}
	return s, nil
}

// parseField compile un champ cron (*, a, a-b, a,b, */n, a-b/n) en l'ensemble des
// valeurs autorisées dans [lo, hi].
func parseField(field string, lo, hi int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		rng := part
		step := 1
		if base, st, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(st)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("pas invalide : %q", part)
			}
			step = n
			rng = base
		}
		start, end := lo, hi
		if rng != "*" {
			if a, b, ok := strings.Cut(rng, "-"); ok {
				var err error
				if start, err = atoiRange(a, lo, hi); err != nil {
					return nil, err
				}
				if end, err = atoiRange(b, lo, hi); err != nil {
					return nil, err
				}
			} else {
				v, err := atoiRange(rng, lo, hi)
				if err != nil {
					return nil, err
				}
				start, end = v, v
			}
		}
		if start > end {
			return nil, fmt.Errorf("plage inversée : %q", part)
		}
		for v := start; v <= end; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("champ vide")
	}
	return out, nil
}

func atoiRange(s string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("valeur invalide : %q", s)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("valeur %d hors bornes [%d-%d]", v, lo, hi)
	}
	return v, nil
}

// cutPrefix reconnaît le préfixe "@every" (insensible à la casse) et renvoie le
// reste. strings.CutPrefix existe (Go 1.20+) mais est sensible à la casse.
func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return "", false
}
