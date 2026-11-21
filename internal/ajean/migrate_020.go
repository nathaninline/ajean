package ajean

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// migrate_020.go — retour d'une installation passée par la 0.20.
//
// La 0.20 ne gardait qu'un moteur, le llama.cpp épinglé, et ignorait la clé BIN.
// La 0.21 relit BIN (engine_resolve.go) et retrouve les réglages de la 0.18 que
// la 0.20 avait figés. Sans cette passe, une installation qui tournait en 0.20
// changerait de comportement en se mettant à jour. Ce qu'elle garantit :
//
//   - les presets Local continuent de tourner sur le moteur épinglé. Ceux créés
//     en 0.20 n'ont pas de ligne BIN : rien à faire. Ceux hérités de la 0.18 en
//     ont une, que la 0.20 ignorait et qui désigne un moteur plus utilisé depuis
//     (voire supprimé : la 0.20 effaçait backends/llama.cpp-prebuilt). Cette
//     ligne est mise en commentaire dans le preset, pas effacée : le chemin
//     reste lisible pour qui veut revenir à ce moteur ;
//   - le mode agent et l'accès internet, « actifs d'office » en 0.20, le restent
//     (en 0.18 comme en 0.21, l'absence de réglage vaut « coupé ») ;
//   - le moteur déjà installé dans backends/engine/<tag>/ est réutilisé tel
//     quel si le tag est celui de cette version : engineInstalled le reconnaît,
//     rien n'est retéléchargé.
//
// Rien d'autre n'est à convertir : les presets Service ont le même format ; les
// presets ExLlama, MoE et GPU cloud, que la 0.20 affichait grisés, n'ont jamais
// été modifiés et redémarrent avec leur moteur s'il est toujours sur le disque ;
// le prompt système par preset (clé sysprompt/<id>), que la 0.20 n'appliquait
// plus, est resté en base et s'applique à nouveau ; le mode d'un appareil que la
// 0.20 a ramené à Projet est un mode de la 0.21. Aucun fichier n'est supprimé.
//
// Comment reconnaître une installation venue de la 0.20 : son moteur épinglé
// est sur le disque avec un marqueur engine.json sans le champ « ajean » (la
// 0.21 l'écrit, la 0.20 non), ou son suivi d'installation est en base (clé
// engine_job, que la 0.21 n'écrit jamais). La passe signe ensuite le marqueur
// et retire la clé : elle ne se rejoue pas, sauf nouveau passage par la 0.20.

// engine020JobKey : l'entête du job d'installation du moteur en 0.20.
const engine020JobKey = "engine_job"

// engines020 : les moteurs épinglés installés par la 0.20 (marqueur non signé).
func engines020() []*engineInfo {
	entries, err := os.ReadDir(engineRoot())
	if err != nil {
		return nil
	}
	var out []*engineInfo
	for _, e := range entries {
		if !e.IsDir() || !reEngineTag.MatchString(e.Name()) || strings.HasSuffix(e.Name(), ".tmp") {
			continue
		}
		if info := readEngineInfo(filepath.Join(engineRoot(), e.Name())); info != nil && info.By == "" {
			out = append(out, info)
		}
	}
	return out
}

// cameFrom020 : cette installation tournait-elle en 0.20 ?
func cameFrom020() bool {
	return len(getBytes(bkState, engine020JobKey)) > 0 || len(engines020()) > 0
}

// bin020Note : le commentaire qui remplace, dans un preset, une ligne BIN que la
// 0.20 ignorait.
const bin020Note = "# BIN ignoré depuis la 0.20 (moteur AJEAN utilisé à la place), gardé pour mémoire : "

// dropStaleBin met en commentaire les lignes BIN d'un preset qui ne désignent
// pas le moteur épinglé. Renvoie le contenu réécrit et vrai s'il a changé.
func dropStaleBin(content string) (string, bool) {
	lines := strings.Split(content, "\n")
	changed := false
	for i, l := range lines {
		trim := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "export "))
		k, v, ok := strings.Cut(trim, "=")
		if !ok || strings.TrimSpace(k) != "BIN" {
			continue
		}
		val := strings.TrimSpace(parseEnv(trim)["BIN"])
		if val == "" {
			val = strings.TrimSpace(v)
		}
		if _, pinned := resolveBin(map[string]string{"BIN": val}); pinned {
			continue // vide, ou déjà dans backends/engine/ : c'est le moteur épinglé
		}
		lines[i] = bin020Note + val
		changed = true
	}
	return strings.Join(lines, "\n"), changed
}

// migrateFrom020 : appelée au démarrage de l'interface et du moteur (voir
// migrateOlderVersions). Sans effet sur une installation qui n'a pas connu la
// 0.20, ni une seconde fois.
func migrateFrom020() {
	if !cameFrom020() {
		return
	}
	// 1. Presets : les lignes BIN héritées de la 0.18 ne reprennent pas la main.
	presets := 0
	if list, err := ListPresets(); err == nil {
		for _, p := range list {
			b, err := os.ReadFile(p.Path)
			if err != nil {
				continue
			}
			if out, changed := dropStaleBin(string(b)); changed {
				if err := os.WriteFile(p.Path, []byte(out), 0o600); err != nil {
					fmt.Fprintf(os.Stderr, "[ajean] reprise 0.20 du preset %s : %v\n", p.ID, err)
					continue
				}
				presets++
			}
		}
	}
	// 2. Configuration active : même règle.
	if cfg := ReadConfig(); strings.TrimSpace(cfg["BIN"]) != "" {
		if _, pinned := resolveBin(cfg); !pinned {
			if err := SetConfigKey("BIN", ""); err != nil {
				fmt.Fprintf(os.Stderr, "[ajean] reprise 0.20 de la configuration active : %v\n", err)
			}
		}
	}
	// 3. Mode agent et accès internet : actifs d'office en 0.20, où seul un arrêt
	// explicite de l'agent était noté ("0"). Sans réglage, ils étaient actifs : on
	// l'écrit, car en 0.21 l'absence de réglage vaut « coupé ». Un "0" reste lu
	// comme « coupé ».
	if getStr(bkState, "agent") == "" {
		_ = setAgentEnabled(true)
	}
	if getStr(bkState, "internet") == "" {
		_ = setInternetEnabled(true)
	}
	// 4. La passe est faite : on signe le moteur de la 0.20 et on retire le suivi
	// de son installation. Le journal ajean-engine.log reste sur le disque.
	for _, e := range engines020() {
		e.By = "0.20"
		if b, err := json.MarshalIndent(e, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(e.dir, engineMarker), append(b, '\n'), 0o644)
		}
	}
	_ = putBytes(bkState, engine020JobKey, nil)
	fmt.Fprintf(os.Stderr, "[ajean] installation 0.20 reprise : %d preset(s) gardé(s) sur le moteur AJEAN, mode agent et accès internet conservés\n", presets)
}

// migrateOlderVersions : les passes de reprise, dans l'ordre des versions.
// Appelée au démarrage de l'interface (ajean web, application de bureau) et du
// service moteur (ajean serve), avant de lire la configuration. Une
// installation 0.18 n'est concernée par aucune des deux : rien n'est lu au-delà
// de la détection, rien n'est écrit.
func migrateOlderVersions() {
	migrateFrom019()
	migrateFrom020()
}
