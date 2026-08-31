package ajean

// mem_index.go — maintien AUTOMATIQUE de l'index MEMORY.md du projet, par le CODE
// (pas par le modèle). À chaque création / suppression / renommage d'une page, on
// ajoute ou retire sa ligne `- [titre](fichier.md)` dans MEMORY.md. L'index reflète
// donc TOUJOURS l'état réel des pages, quel que soit le modèle — le modèle peut
// seulement enrichir l'accroche après le titre, on ne la touche jamais.
//
// Best-effort : si la mémoire est chiffrée et verrouillée (lecture/écriture de
// MEMORY.md impossible), on n'échoue pas — l'index sera re-synchronisé au prochain
// passage déverrouillé, et la consigne du prompt reste un filet.

import (
	"strings"
)

const memIndexFile = "MEMORY.md"

// memIndexPrefix marque le message d'index injecté dans l'historique, pour le
// détecter et éviter les doublons.
const memIndexPrefix = "Project memory index"

// memIndexMessage construit le message système d'index à injecter UNE FOIS au début
// de la conversation (et après un compactage). Renvoie ok=false hors mode auto ou
// si l'index est vide. On n'injecte que l'INDEX (titres + accroches), jamais le
// contenu des pages — l'IA fait mem_read pour lire une page.
func memIndexMessage() (Message, bool) {
	if memMode() != MemAlways {
		return Message{}, false
	}
	idx := strings.TrimSpace(MemContent(memIndexFile))
	if idx == "" {
		return Message{}, false
	}
	content := memIndexPrefix + " for project \"" + projectName(activeProjectSlug()) +
		"\" — the pages you can open with mem_read (only titles/hooks here, not their content). Auto-maintained.\n\n" + idx
	return Message{Role: "system", Content: content}, true
}

// hasMemIndex indique si la séquence porte déjà le message d'index.
func hasMemIndex(msgs []Message) bool {
	for _, m := range msgs {
		if s, ok := m.Content.(string); ok && strings.HasPrefix(s, memIndexPrefix) {
			return true
		}
	}
	return false
}

// ensureMemIndexFront préfixe l'index s'il n'est pas déjà présent. Appelé après un
// compactage (le message d'index d'origine a pu être résumé/retiré).
func ensureMemIndexFront(msgs []Message) []Message {
	if hasMemIndex(msgs) {
		return msgs
	}
	if m, ok := memIndexMessage(); ok {
		return append([]Message{m}, msgs...)
	}
	return msgs
}

// isIndexFile indique si `name` est le fichier d'index (à ne jamais indexer lui-même).
func isIndexFile(name string) bool {
	return strings.EqualFold(strings.TrimSpace(name), memIndexFile)
}

// indexLineFor construit la ligne d'index d'une page : `- [titre](fichier.md)`. Le
// titre vient de la 1re ligne de la page (titleOf), à défaut le nom du fichier.
func indexLineFor(name string) string {
	title := name
	if b, err := memReadPage(name); err == nil {
		if t := titleOf(string(b)); t != "" {
			title = t
		}
	}
	return "- [" + title + "](" + name + ")"
}

// indexRefFor est le motif qui identifie la ligne d'une page dans l'index :
// `](fichier.md)`. Sert à retrouver/retirer la ligne existante sans ambiguïté (le
// nom complet + la parenthèse fermante évitent qu'un suffixe en matche un autre).
func indexRefFor(name string) string { return "](" + name + ")" }

// memIndexAdd ajoute la ligne d'une page à MEMORY.md si elle n'y est pas déjà. Si
// une ligne pour ce fichier existe (l'IA a pu écrire une accroche), on la LAISSE.
func memIndexAdd(name string) {
	fn, err := memFileName(name)
	if err != nil || isIndexFile(fn) {
		return
	}
	ensureIndexSeed()
	cur, err := memReadPage(memIndexFile)
	if err != nil {
		return // chiffré+verrouillé ou illisible : best-effort
	}
	content := string(cur)
	ref := indexRefFor(fn)
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, ref) {
			return // déjà indexée
		}
	}
	body := strings.TrimRight(content, "\n") + "\n" + indexLineFor(fn) + "\n"
	_ = writeMemFile(memIndexFile, []byte(body))
}

// memIndexRemove retire la (les) ligne(s) d'une page de MEMORY.md.
func memIndexRemove(name string) {
	fn, err := memFileName(name)
	if err != nil || isIndexFile(fn) {
		return
	}
	cur, err := memReadPage(memIndexFile)
	if err != nil {
		return
	}
	ref := indexRefFor(fn)
	lines := strings.Split(string(cur), "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for _, line := range lines {
		if strings.Contains(line, ref) {
			changed = true
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return
	}
	body := strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
	_ = writeMemFile(memIndexFile, []byte(body))
}

// memIndexRename retire l'ancienne ligne et ajoute la nouvelle (renommage d'une page).
func memIndexRename(oldName, newName string) {
	if oldName != "" {
		memIndexRemove(oldName)
	}
	memIndexAdd(newName)
}

// reconcileMemIndex complète l'index du projet ACTIF : pour chaque page existante
// non référencée dans MEMORY.md, il ajoute sa ligne. ADDITIF (ne touche pas aux
// lignes/accroches déjà présentes). Indispensable après une migration : les pages
// déplacées à la main sur le disque n'ont jamais été indexées (l'index n'est
// alimenté que par mem_add). Best-effort : ne fait rien si la mémoire est chiffrée
// et verrouillée (l'index sera complété au prochain accès déverrouillé).
func reconcileMemIndex() {
	pages := mdPages() // pages du projet actif
	if len(pages) == 0 {
		return
	}
	ensureIndexSeed()
	cur, err := memReadPage(memIndexFile)
	if err != nil {
		return // chiffré+verrouillé ou illisible
	}
	content := string(cur)
	var add []string
	for _, name := range pages {
		if isIndexFile(name) {
			continue
		}
		if strings.Contains(content, indexRefFor(name)) {
			continue // déjà indexée
		}
		add = append(add, indexLineFor(name))
	}
	if len(add) == 0 {
		return
	}
	body := strings.TrimRight(content, "\n") + "\n" + strings.Join(add, "\n") + "\n"
	_ = writeMemFile(memIndexFile, []byte(body))
}

// ensureIndexSeed crée MEMORY.md (avec son en-tête) s'il n'existe pas encore, pour
// que l'ajout d'une première ligne ait un fichier où s'écrire.
func ensureIndexSeed() {
	if _, err := memReadPage(memIndexFile); err == nil {
		return // existe déjà (et lisible)
	}
	// Absent (ou illisible car chiffré-verrouillé — dans ce cas writeMemFile
	// échouera aussi, sans dommage). On sème l'en-tête du projet actif.
	_ = writeMemFile(memIndexFile, []byte(memorySeed(projectName(activeProjectSlug()))))
}
