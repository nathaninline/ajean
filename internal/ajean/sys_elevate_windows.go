//go:build windows

package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Élévation ponctuelle pour terminer la migration du dossier de données.
//
// Renommer %ProgramData%\jean exige d'écrire dans %ProgramData%, ce qu'un compte
// standard ne peut pas faire. Sans élévation, ces postes gardent l'ancien nom
// pour toujours — ce qui est sans conséquence, mais laisse le renommage à
// moitié fait.
//
// La fenêtre UAC n'apparaît QUE si elle sert à quelque chose :
//
//   - migration déjà faite, inutile, ou chemin imposé par l'utilisateur → rien ;
//   - migration en attente, dans un parcours d'installation → on demande.
//
// On ne cherche PAS à deviner la cause depuis le code d'erreur : Windows renvoie
// ERROR_ACCESS_DENIED aussi bien pour un manque de droits que pour un dossier
// verrouillé par un programme en cours. Impossible de les séparer — vérifié.
//
// C'est donc le LIEU D'APPEL qui fait la sélection, et lui est fiable : on
// n'élève que depuis `install` et depuis l'installeur de mise à jour, tous deux
// après l'arrêt des instances en cours. Le cas « verrouillé » y est déjà écarté ;
// ce qui reste est un problème de droits, que l'élévation résout.

// migrateHomeArg est la sous-commande interne exécutée par le process élevé.
// Absente de l'aide : ce n'est pas une commande destinée aux utilisateurs, mais
// le travail précis qu'on délègue à un process ayant les droits.
const migrateHomeArg = "migrate-home-elevated"

// cmdMigrateHomeElevated est le point d'entrée du process élevé : il migre, dit
// ce qu'il a fait, et rend la main. Rien d'autre — moins il en fait sous des
// droits administrateur, mieux c'est.
func cmdMigrateHomeElevated() error {
	if retryHomeMigration() {
		fmt.Printf("%s dossier de données migré vers %s\n", green("✓"), AjeanHome())
		return nil
	}
	if migrationDeferred != "" {
		return fmt.Errorf("migration toujours impossible : %s", migrationDeferred)
	}
	return nil
}

// elevateForHomeMigration relance AJEAN en administrateur le temps de la seule
// migration, puis revérifie. Renvoie true si le dossier a effectivement changé.
//
// Silencieux et sans effet quand l'élévation ne servirait à rien, y compris si
// l'utilisateur refuse la fenêtre UAC : son refus est une réponse valable, la
// machine continue de fonctionner sur l'ancien chemin.
func elevateForHomeMigration() bool {
	if migrationDeferred == "" {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	// Start-Process -Verb RunAs déclenche l'invite UAC ; -Wait pour que la suite
	// de l'installation reparte sur le bon dossier, pas sur l'ancien.
	ps := fmt.Sprintf(
		`try { Start-Process -FilePath %s -ArgumentList '%s' -Verb RunAs -Wait -WindowStyle Hidden; 'ok' } catch { 'refus' }`,
		psQuote(exe), migrateHomeArg)
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-Command", ps)).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		return false // refus de l'utilisateur, ou élévation indisponible
	}
	// Le process élevé a fait le travail dans SON espace mémoire : on relit le
	// disque pour savoir ce qu'il en est réellement ici.
	migrationDeferred = ""
	return retryHomeMigration()
}
