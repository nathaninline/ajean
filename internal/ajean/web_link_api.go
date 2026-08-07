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
	_ = uiServiceCtl("stop")
	_ = removeLinkToken()
	sendJSON(w, http.StatusOK, map[string]any{"ok": true, "linked": false, "active": false})
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
