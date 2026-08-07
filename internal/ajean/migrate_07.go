package ajean

// migrate_07.go — CODE TEMPORAIRE, à supprimer d'ici une version ou deux.
//
// Reprend une installation 0.7.x au moment de `ajean install`. Tout est ici, et
// rien d'autre n'en dépend : le jour où le parc aura basculé, supprimer ce
// fichier et les deux appels à migrateFrom07() (sys_install_unix.go et
// sys_install_windows.go) suffit. C'est la seule raison d'être de ce fichier —
// la 0.8 ne contient AUCUN autre code de compatibilité.
//
// POURQUOI ICI, ET PAS AU DÉMARRAGE. La version précédente migrait à chaque
// lancement. Sur un parc, ça veut dire une réécriture d'unités systemd à chaque
// boot, et un échec laisse une machine distante sans service et sans terminal
// pour réparer. `install` est le seul bon moment : root, délibéré, et il écrit
// déjà les unités.
//
// CE QU'ON NE DÉTRUIT PAS. Les presets, la mémoire et les modèles sont
// DÉPLACÉS, jamais copiés ni effacés. Les petits fichiers d'état sont lus puis
// rangés dans avant-0.8/ — ils ne servent plus, mais ils sont là si quelque
// chose a mal tourné.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// legacyUnits : les noms de services qu'a portés le produit avant la 0.8.
//
// Les désactiver n'est pas cosmétique. Une unité `jean.service` restée active
// relance un SECOND llama-server au prochain boot : deux moteurs sur le port
// 8080, la VRAM prise deux fois, et un démarrage qui échoue à moitié sans
// raison apparente.
var legacyUnits = []string{"jean", "jean-link", "ajean", "ajean-link"}

// hasEntry dit si home contient une entrée portant EXACTEMENT ce nom.
//
// os.Stat ne convient pas : sous Windows et macOS le système de fichiers est
// insensible à la casse, donc Stat("MEMORY") réussit sur un dossier nommé
// « memory ». Une installation 0.8 toute neuve passait ainsi pour une 0.7, et
// la migration se redéclenchait à chaque `install`. On compare donc les noms
// réels, tels que le système de fichiers les rapporte.
func hasEntry(home, name string) bool {
	entries, err := os.ReadDir(home)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// sameDir dit si deux chemins désignent le MÊME dossier — ce qui arrive quand
// ils ne diffèrent que par la casse sur un système de fichiers qui l'ignore.
func sameDir(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// needsMigration07 dit si le dossier de données est encore dans l'ancienne
// disposition. On se fie à ce qui ne peut PAS exister en 0.8.
func needsMigration07(home string) bool {
	return hasEntry(home, "config.env") || hasEntry(home, "configs") || hasEntry(home, "MEMORY")
}

// migrateFrom07 reprend une installation 0.7.x. Sans effet si le dossier est
// déjà en 0.8. Doit être appelée AVANT provisionDataDir : elle déplace des
// dossiers entiers, ce qui suppose que la destination n'existe pas encore.
func migrateFrom07(home string) error {
	if !needsMigration07(home) {
		return nil
	}
	fmt.Printf("\n%s installation 0.7 détectée — reprise en cours\n", cyan("[migration]"))

	// 1. Arrêter les anciens services : ils tiennent les fichiers qu'on déplace,
	//    et sous Windows un .gguf ouvert par llama-server refuse d'être renommé.
	stopLegacyUnits()

	// 2. Les dossiers. Un rename sur le même volume est instantané, même pour
	//    130 Go de modèles, et atomique : jamais d'état à moitié déplacé.
	for _, m := range []struct{ from, to string }{
		{"configs", "presets"},
		{"MEMORY", "memory"},
	} {
		if !hasEntry(home, m.from) { // nom exact : « MEMORY » n'est pas « memory »
			continue
		}
		src, dst := filepath.Join(home, m.from), filepath.Join(home, m.to)
		// « configs » et « presets » ne se confondent pas, mais « MEMORY » et
		// « memory » si : sur un système insensible à la casse, ouvrir la
		// destination ouvre en réalité la source. On ne considère donc la
		// destination comme distincte que si c'est un AUTRE dossier.
		if !sameDir(src, dst) {
			// Destination déjà remplie : la machine a été migrée à la main et
			// l'ancien dossier n'a pas été retiré. Écraser détruirait le travail
			// fait, fusionner devinerait qui gagne. On laisse les deux et on le
			// dit — c'est le seul cas où quelqu'un doit trancher.
			if n, _ := os.ReadDir(dst); len(n) > 0 {
				fmt.Printf("  %s %s/ ET %s/ existent tous les deux — %s/ laissé tel quel, à supprimer à la main\n",
					yellow("[info]"), m.from, m.to, m.from)
				continue
			}
			_ = os.Remove(dst) // destination vide créée par une install précédente
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("%s → %s : %w", m.from, m.to, err)
		}
		fmt.Printf("  %s %s/ → %s/\n", green("✓"), m.from, m.to)
	}
	if n, err := moveModels(home); err != nil {
		return err
	} else if n > 0 {
		fmt.Printf("  %s %d modèle(s) → models/\n", green("✓"), n)
	}

	// 3. Les réglages, vers la base.
	if err := importLegacyState(home); err != nil {
		return err
	}
	fmt.Printf("%s reprise terminée — les anciens fichiers sont dans avant-0.8/\n\n", green("[ok]"))
	return nil
}

// stopLegacyUnits est une variable pour que le test de migration n'aille pas
// invoquer systemctl sur la machine qui exécute les tests.
var stopLegacyUnits = stopLegacyUnitsReal

// stopLegacyUnitsReal arrête et désactive les services d'avant la 0.8,
// silencieux sur ceux qui n'existent pas (le cas courant).
func stopLegacyUnitsReal() {
	for _, u := range legacyUnits {
		switch runtime.GOOS {
		case "linux":
			_ = exec.Command("systemctl", "stop", u).Run()
			_ = exec.Command("systemctl", "disable", u).Run()
		case "darwin":
			for _, label := range []string{"com.jean." + u, "com.ajean." + u} {
				_ = exec.Command("launchctl", "unload", "-w", "/Library/LaunchDaemons/"+label+".plist").Run()
			}
		}
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "daemon-reload").Run()
	}
}

// moveModels range les .gguf de la racine dans models/. Renvoie le nombre
// déplacé. Un fichier qui résiste (encore ouvert) interrompt la migration : le
// laisser derrière donnerait une installation à laquelle il manque un modèle,
// sans le dire.
func moveModels(home string) (int, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return 0, err
	}
	dst := filepath.Join(home, "models")
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".gguf") {
			continue
		}
		if n == 0 {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return 0, err
			}
		}
		if err := os.Rename(filepath.Join(home, name), filepath.Join(dst, name)); err != nil {
			return n, fmt.Errorf("déplacement de %s : %w", name, err)
		}
		n++
	}
	return n, nil
}

// importLegacyState recopie les anciens fichiers d'état dans la base, puis les
// range dans avant-0.8/. Les clés et les buckets sont ceux de store.go.
func importLegacyState(home string) error {
	old := filepath.Join(home, "avant-0.8")
	if err := os.MkdirAll(old, 0o755); err != nil {
		return err
	}
	consumed := []string{}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(home, name))
		if err != nil {
			return ""
		}
		consumed = append(consumed, name)
		return string(b)
	}

	// La configuration d'abord : sans elle le moteur ne démarre pas.
	if cfg := parseEnv(read("config.env")); len(cfg) > 0 {
		if err := WriteConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("  %s configuration (%d clés)\n", green("✓"), len(cfg))
	}
	if prefs := map[string]string{}; json.Unmarshal([]byte(read("webprefs.json")), &prefs) == nil && len(prefs) > 0 {
		_ = replaceKV(bkPrefs, prefs)
	}
	if conv := read("conversation.json"); conv != "" {
		_ = putBytes(bkChat, "conversation", []byte(conv))
		fmt.Printf("  %s conversation\n", green("✓"))
	}

	// Clés et jetons : le contenu du fichier, débarrassé de sa fin de ligne.
	for key, name := range map[string]string{
		"api_key":      ".api_key",
		"web_key":      ".web_key",
		"crawl_key":    ".crawl4ai_key",
		"link_token":   ".link_token",
		"link_machine": ".link_machine",
	} {
		if v := strings.TrimSpace(read(name)); v != "" {
			_ = putStr(bkState, key, v)
		}
	}
	// Drapeaux : seule l'EXISTENCE du fichier comptait.
	for key, name := range map[string]string{
		"agent":      ".agent_enabled",
		"internet":   ".internet_enabled",
		"oai_public": ".oai_public",
	} {
		if _, err := os.Stat(filepath.Join(home, name)); err == nil {
			_ = putBool(bkState, key, true)
			consumed = append(consumed, name)
		}
	}
	// Documents JSON repris tels quels.
	for key, name := range map[string]string{
		"model_dirs":    "model_dirs.json",
		"last_bench":    ".last_bench.json",
		"bench_presets": ".bench_presets.json",
		"sysprompt":     "sysprompt.txt",
	} {
		if v := read(name); v != "" {
			if key == "sysprompt" {
				_ = putStr(bkState, key, strings.TrimSpace(v))
			} else {
				_ = putBytes(bkState, key, []byte(v))
			}
		}
	}
	// mcp.json : on ne garde que la map interne, la clé « mcpServers » disparaît.
	if v := read("mcp.json"); v != "" {
		var f struct {
			MCPServers json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal([]byte(v), &f) == nil && len(f.MCPServers) > 0 {
			_ = putBytes(bkState, "mcp", f.MCPServers)
		}
	}

	for _, name := range consumed {
		_ = os.Rename(filepath.Join(home, name), filepath.Join(old, name))
	}
	return nil
}
