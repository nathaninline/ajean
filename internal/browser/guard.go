package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ajean/internal/netguard"
)

const lookupTimeout = 5 * time.Second // résolution du nom d'hôte d'une adresse à contrôler

// checkURL dit si le navigateur a le droit de se rendre à une adresse : http(s)
// seulement, jamais la machine locale ni, sauf AJEAN_TOOLS_LAN, le réseau local.
// Le proxy refuserait de toute façon la connexion ; ce contrôle écarte les
// schémas qui ne passent pas par le réseau (file:, chrome:, data:…) et donne
// au modèle la raison du refus.
func checkURL(raw string) error {
	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()
	return netguard.CheckURL(ctx, raw)
}

// onEvent tourne dans la boucle de lecture : toute réponse au navigateur part
// dans une goroutine, sinon elle attendrait un message que plus personne ne lit.
func (b *instance) onEvent(c *conn, m message) {
	switch m.Method {
	case "Target.attachedToTarget":
		go b.equip(c, m)
	case "Page.javascriptDialogOpening": // alert/confirm/prompt : sans réponse, la page reste figée
		var p struct{ Type, Message string }
		_ = json.Unmarshal(m.Params, &p)
		b.note(fmt.Sprintf("dialogue %s de la page, accepté automatiquement : %q", p.Type, clip(p.Message, 300)))
		go func() {
			_ = c.call(m.SessionID, "Page.handleJavaScriptDialog", map[string]any{"accept": true}, nil, callTimeout)
		}()
	case "Browser.downloadWillBegin":
		var p struct{ SuggestedFilename string }
		_ = json.Unmarshal(m.Params, &p)
		b.note("téléchargement lancé : " + p.SuggestedFilename + " (dans downloads/ du workspace)")
	case "Page.fileChooserOpened":
		b.note("la page a demandé un fichier : l'envoi de fichiers n'est pas disponible")
	}
}

// equip prépare un onglet à sa naissance : le navigateur nous l'attache figé,
// avant son premier script. Les réglages partent d'abord, l'ordre de reprise
// ensuite (figé, l'onglet ne répond à rien) : un onglet ouvert par un clic ne
// peut donc ni rester bloqué sur un dialogue que personne n'a vu, ni ouvrir le
// sélecteur de fichiers du système.
func (b *instance) equip(c *conn, m message) {
	var p struct {
		SessionID  string
		TargetInfo struct{ TargetID string }
	}
	_ = json.Unmarshal(m.Params, &p)
	setup := []func(any, time.Duration) error{
		c.send(p.SessionID, "Page.enable", nil),                                                    // pour recevoir les dialogues
		c.send(p.SessionID, "Page.setInterceptFileChooserDialog", map[string]any{"enabled": true}), // jamais de sélecteur de fichier du système
		c.send(p.SessionID, "Runtime.runIfWaitingForDebugger", nil),
	}
	for _, wait := range setup {
		if wait(nil, callTimeout) != nil { // un onglet mal préparé ne se pilote pas
			_ = c.call("", "Target.closeTarget", map[string]any{"targetId": p.TargetInfo.TargetID}, nil, callTimeout)
			return
		}
	}
	b.mu.Lock()
	b.sessions[p.TargetInfo.TargetID] = p.SessionID
	b.mu.Unlock()
}
