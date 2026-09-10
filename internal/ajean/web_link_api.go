package ajean

// web_link_api.go — API locale pilotant l'accès distant (ajean.link) depuis l'UI
// web de AJEAN, pour éviter le terminal. Le panneau « Accès distant » ouvre une
// popup app.ajean.link/connect.html qui gère compte + paiement puis renvoie une
// clé de liaison (jl_…) ; l'UI la POSTe ici, et AJEAN fait le `ajean link` tout
// seul (écrit le token + (re)démarre le service).
//
// Ces routes ne sont accessibles qu'en LOCAL : à travers le tunnel du relais,
// newLinkHandler refuse tout /api/* non chiffré (boîte noire). La configuration
// de l'accès distant se fait donc depuis la machine elle-même, ce qui est correct.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// remoteServerURL est l'URL du portail distant pointant droit sur cette machine.
func remoteServerURL() string {
	return "https://app.ajean.link/server.html?m=" + machineID()
}

// handleLinkStatus (GET /api/link/status) : état de l'accès distant pour l'UI.
func handleLinkStatus(w http.ResponseWriter, r *http.Request) {
	tok := readLinkToken()
	sendJSON(w, http.StatusOK, map[string]any{
		"linked":      tok != "",
		"active":      uiServiceActive(),
		"machineURL":  remoteServerURL(),
		"fingerprint": e2eFingerprint(),
	})
}

// handleLinkConnect (POST /api/link/connect {token}) : enregistre la clé de
// liaison remise par la popup connect.html et (re)démarre le service de lien.
func handleLinkConnect(w http.ResponseWriter, r *http.Request) {
	var req struct{ Token string }
	_ = json.NewDecoder(r.Body).Decode(&req)
	tok := strings.TrimSpace(req.Token)
	if !strings.HasPrefix(tok, "jl_") {
		sendJSON(w, http.StatusBadRequest, map[string]any{"error": "clé de liaison invalide"})
		return
	}
	if err := saveLinkToken(tok); err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Redémarre le service d'interface pour qu'il relise la clé et ouvre le
	// tunnel. Sur une machine sans systemd (dev/Windows), la clé est quand même
	// enregistrée : on renvoie l'info en signalant que le service n'a pas démarré.
	svcErr := ""
	if err := uiServiceCtl("restart"); err != nil {
		svcErr = err.Error()
	}
	sendJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"linked":     true,
		"active":     uiServiceActive(),
		"machineURL": remoteServerURL(),
		"serviceErr": svcErr,
	})
}

// handleLinkStart (POST /api/link/start) : (re)démarre le worker de lien sans
// repasser par la popup de connexion — le token est déjà enregistré. Utile quand
// le tunnel est tombé, ou sur un poste sans systemd où personne ne le relance.
func handleLinkStart(w http.ResponseWriter, r *http.Request) {
	if readLinkToken() == "" {
		sendJSON(w, http.StatusBadRequest, map[string]any{"error": "aucune clé de liaison enregistrée"})
		return
	}
	if err := uiServiceCtl("restart"); err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error(), "active": uiServiceActive()})
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "active": uiServiceActive()})
}

// handleLinkDisconnect (POST /api/link/disconnect) : arrête le service et oublie
// la clé (équivalent `ajean link stop` + `ajean link logout`).
func handleLinkDisconnect(w http.ResponseWriter, r *http.Request) {
	// On OUBLIE le jeton d'abord : c'est l'action qui compte, et elle doit réussir
	// même si le (re)démarrage qui suit tue ce process en route.
	if err := removeLinkToken(); err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// On RÉPOND avant de toucher au service. L'ancien code arrêtait le service
	// AVANT de répondre : le process mourait en plein milieu, la réponse n'arrivait
	// jamais, et l'UI tombait sur un « JSON.parse » de réponse vide (« SyntaxError »).
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "linked": false, "active": true})
	// Puis on REDÉMARRE (pas « stop ») en arrière-plan, après un court délai laissant
	// la réponse partir : le process relit un jeton vide → plus de tunnel vers le
	// relais, mais l'UI locale (:8090) reste servie. « stop » coupait aussi l'UI
	// locale, puisque le même process sert les deux.
	go func() {
		time.Sleep(400 * time.Millisecond)
		if err := uiServiceCtl("restart"); err != nil {
			fmt.Printf("%s redémarrage après déconnexion du lien: %v\n", red("[ERREUR]"), err)
		}
	}()
}

// handleLinkPairCode (POST /api/link/paircode) : génère un code d'appairage frais
// (usage unique, TTL 10 min) + l'empreinte E2E, à saisir UNE fois dans le portail
// distant lors de la première connexion (confirmation de la boîte noire). Évite le
// détour par `ajean link code` en terminal.
func handleLinkPairCode(w http.ResponseWriter, r *http.Request) {
	code, err := newPairCode()
	if err != nil {
		sendJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("génération du code (droits ?): %v", err)})
		return
	}
	sendJSON(w, http.StatusOK, map[string]any{"code": code, "fingerprint": e2eFingerprint()})
}
