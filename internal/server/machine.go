package server

import (
	"errors"
	"net/http"
	"slices"
	"strconv"

	"ajean/internal/config"
	"ajean/internal/machine"
)

// handleMachine : la mesure de la machine et l'état des services suivis.
// L'écran Machine l'appelle toutes les deux secondes tant qu'il est ouvert.
func handleMachine(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]any{"stats": machine.Read(), "services": machine.Status(config.Get().Services)})
}

// handleServicesAvailable : les services de l'utilisateur, pour en ajouter un.
func handleServicesAvailable(w http.ResponseWriter, r *http.Request) {
	reply(w, map[string]any{"units": machine.Available()})
}

// handleServiceTrack ajoute (ou retire) un service de l'écran Machine. Le
// service lui-même n'est pas touché.
func handleServiceTrack(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Unit   string
		Remove bool
	}
	if !body(w, r, &q) {
		return
	}
	unit, err := machine.Unit(q.Unit)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	done(w, config.Update(func(c *config.Config) error {
		i := slices.Index(c.Services, unit)
		switch {
		case q.Remove && i >= 0:
			c.Services = slices.Delete(c.Services, i, i+1)
		case !q.Remove && i < 0:
			c.Services = append(c.Services, unit)
		}
		return nil
	}), http.StatusInternalServerError)
}

// tracked : le service doit être suivi pour être piloté ou lu.
func tracked(name string) (string, error) {
	unit, err := machine.Unit(name)
	if err != nil {
		return "", err
	}
	if !slices.Contains(config.Get().Services, unit) {
		return "", errors.New("service non suivi : ajoute-le d'abord à l'écran Machine")
	}
	return unit, nil
}

// handleServiceAct démarre, arrête ou redémarre un service suivi.
func handleServiceAct(w http.ResponseWriter, r *http.Request) {
	var q struct{ Unit, Action string }
	if !body(w, r, &q) {
		return
	}
	unit, err := tracked(q.Unit)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	done(w, machine.Act(unit, q.Action), http.StatusBadRequest)
}

// handleServiceLogs : les dernières lignes du journal d'un service suivi.
func handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	unit, err := tracked(r.URL.Query().Get("unit"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	text, err := machine.Logs(unit, n)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, map[string]string{"text": text})
}

// handleServiceGet : la définition d'un service (commande, dossier, variables…).
func handleServiceGet(w http.ResponseWriter, r *http.Request) {
	unit, err := machine.Unit(r.URL.Query().Get("unit"))
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	d, err := machine.Load(unit)
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	reply(w, d)
}

// handleServiceSave crée ou modifie un service, le suit, et le relance si demandé.
func handleServiceSave(w http.ResponseWriter, r *http.Request) {
	var q struct {
		machine.Def
		Apply bool `json:"apply"`
	}
	if !body(w, r, &q) {
		return
	}
	unit, err := machine.Unit(q.Unit)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	q.Def.Unit = unit
	if err := machine.Save(q.Def); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := config.Update(func(c *config.Config) error {
		if !slices.Contains(c.Services, unit) {
			c.Services = append(c.Services, unit)
		}
		return nil
	}); err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	if q.Apply {
		if err := machine.Apply(unit); err != nil {
			fail(w, http.StatusBadRequest, err)
			return
		}
	}
	ok(w)
}

// handleServiceRemove arrête un service suivi, supprime son fichier et ne le suit plus.
func handleServiceRemove(w http.ResponseWriter, r *http.Request) {
	var q struct{ Unit string }
	if !body(w, r, &q) {
		return
	}
	unit, err := tracked(q.Unit)
	if err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	if err := machine.Remove(unit); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	done(w, config.Update(func(c *config.Config) error {
		c.Services = slices.DeleteFunc(c.Services, func(s string) bool { return s == unit })
		return nil
	}), http.StatusInternalServerError)
}
