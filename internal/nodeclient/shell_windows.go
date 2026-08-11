package nodeclient

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCmd construit la commande shell sur Windows : cmd.exe, fenêtre cachée.
//
// ⚠️ On passe la ligne de commande BRUTE via CmdLine plutôt que par Args. Sinon
// Go ré-échappe les guillemets internes (EscapeArg) et cmd.exe reçoit une ligne
// mal formée : `dir "C:\Users\X"` renvoyait « syntaxe du nom de fichier
// incorrecte », alors que la même commande sans guillemets marchait. Avec
// `cmd /S /C "<commande>"`, l'option /S dit à cmd de ne retirer QUE le premier et
// le dernier guillemet et d'exécuter le reste tel quel — les chemins entre
// guillemets (avec espaces) passent alors correctement.
func shellCmd(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd")
	// `chcp 65001>nul &` force la sortie de cmd en UTF-8. Sinon cmd émet dans la
	// codepage OEM (CP850 en FR) : les accents (« Répertoire », « numéro ») et le
	// séparateur de milliers arrivaient en octets non-UTF-8 → rendus « � » une fois
	// sérialisés en JSON. Avec /S, cmd ne retire que le 1er et le dernier guillemet,
	// donc les guillemets internes de la commande passent intacts.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow: true,
		CmdLine:    `cmd /S /C "chcp 65001>nul & ` + command + `"`,
	}
	return cmd
}
