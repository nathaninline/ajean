// Package nodewire porte le protocole PARTAGÉ entre le serveur ajean et le
// client léger « poste distant » (ajean-node). Zéro dépendance hors stdlib :
// c'est ce qui permet au client de rester minuscule (il n'embarque ni l'UI, ni
// llama.cpp, ni bbolt, ni MCP — juste ce fichier + coder/websocket).
//
// Le format du fil (Msg + capacités) DOIT rester identique des deux côtés :
// centraliser ici évite toute dérive entre serveur et client.
package nodewire

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Capacités qu'un poste peut exposer. Chacune est un pouvoir concret donné à
// l'IA sur la machine distante.
const (
	CapShell = "shell" // exécuter une commande shell
	CapRead  = "read"  // lire un fichier (confiné à la racine)
	CapWrite = "write" // écrire un fichier (confiné à la racine)
	CapList  = "list"  // lister un dossier (confiné à la racine)
	// CapFetch RAPATRIE un fichier binaire du poste (par tranches base64) pour que
	// l'IA puisse te LIVRER un fichier créé à distance, téléchargeable dans le chat.
	// Ce n'est PAS une capacité à part accordée au pairage : c'est une forme de
	// lecture, donc autorisée partout où `read` l'est (voir CapAuthFor). Elle ne
	// figure donc pas dans AllCaps — inutile de re-pairer un poste existant.
	CapFetch = "fetch"
)

// CapAuthFor renvoie la capacité RÉELLEMENT requise pour autoriser `cap`. Un
// `fetch` (téléchargement) est un `read` privilégié : on l'autorise là où la
// lecture l'est, sans l'ajouter aux listes de capacités (pas de re-pairage).
func CapAuthFor(cap string) string {
	if cap == CapFetch {
		return CapRead
	}
	return cap
}

// AllCaps est l'ordre canonique d'affichage/itération.
var AllCaps = []string{CapShell, CapRead, CapWrite, CapList}

// CapLabel donne un libellé court en français.
func CapLabel(cap string) string {
	switch cap {
	case CapShell:
		return "shell"
	case CapRead:
		return "lire des fichiers"
	case CapWrite:
		return "écrire des fichiers"
	case CapList:
		return "lister des dossiers"
	case CapFetch:
		return "télécharger un fichier"
	}
	return cap
}

// Msg est l'unique enveloppe échangée sur le WebSocket, dans les deux sens.
//   - client→serveur au démarrage : {type:"hello", name, os, caps:[…]}
//   - serveur→client : {type:"call", id, cap, args}
//   - client→serveur : {type:"result", id, result}
type Msg struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Cap    string         `json:"cap,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result string         `json:"result,omitempty"`
	Name   string         `json:"name,omitempty"`
	OS     string         `json:"os,omitempty"`
	Caps   []string       `json:"caps,omitempty"`
}

// CapAllowed indique si cap figure dans allowed.
func CapAllowed(allowed []string, cap string) bool {
	for _, c := range allowed {
		if c == cap {
			return true
		}
	}
	return false
}

// CapIntersect renvoie, dans l'ordre canonique, les capacités présentes dans les
// deux listes — le jeu EFFECTIF exposé à l'IA (propriétaire ∩ poste).
func CapIntersect(allowed, declared []string) []string {
	var out []string
	for _, c := range AllCaps {
		if CapAllowed(allowed, c) && CapAllowed(declared, c) {
			out = append(out, c)
		}
	}
	return out
}

// SanitizeCaps ne garde que des capacités connues, sans doublon, dans l'ordre
// canonique. Toute entrée inconnue est ignorée.
func SanitizeCaps(caps []string) []string {
	var out []string
	for _, c := range AllCaps {
		if CapAllowed(caps, c) {
			out = append(out, c)
		}
	}
	return out
}

// ResolvePath résout un chemin demandé À L'INTÉRIEUR de la racine autorisée et
// REFUSE tout ce qui en sortirait (traversée « .. », absolu hors racine). C'est
// le confinement des outils fichiers.
func ResolvePath(root, p string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("aucun dossier racine configuré sur ce poste")
	}
	if strings.TrimSpace(p) == "" {
		return "", errors.New("chemin vide")
	}
	root = filepath.Clean(root)
	var abs string
	if filepath.IsAbs(p) {
		abs = filepath.Clean(p)
	} else {
		abs = filepath.Clean(filepath.Join(root, p))
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("chemin invalide: %v", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("chemin hors du dossier autorisé (%s)", root)
	}
	return abs, nil
}
