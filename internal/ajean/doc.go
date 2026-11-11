// AJEAN — cœur du binaire (package ajean, appelé par cmd/ajean). Go n'autorise
// pas de sous-dossiers dans un même package : les fichiers sont donc préfixés
// par domaine.
//
//	run.go        point d'entrée : dispatch des sous-commandes, AjeanHome() et
//	              l'arborescence de données
//	store.go      LA base (bbolt) : configuration, préférences, conversation,
//	              clés, jetons, interrupteurs — tout l'état non éditable à la main
//	cli_*         expérience « application » (double-clic : UI + tray + splash)
//	web_*         serveur HTTP :8090 (UI embarquée via go:embed ui/, auth, prefs)
//	chat_*        chat CLI + conversation serveur partagée, compaction, mémoire,
//	              outils de l'agent (dont accès internet)
//	llm_*         client du modèle du chat (complétions, endpoint OpenAI, bench, test)
//	services*     les modèles : des services système qu'AJEAN démarre, arrête et
//	              surveille (définitions, unités par OS, garde-fou, CLI, API)
//	backend_*     configuration, API externe, sondes GPU, anciens presets (conversion)
//	relay_*       accès distant ajean.link : tunnel, chiffrement E2E, appairage
//	sys_*         intégration OS : install, services, plateforme, process, tray,
//	              splash, tty, auto-update
//
// Les suffixes _windows/_linux/_darwin/_unix/_other portent les contraintes de
// compilation par OS.
//
// ⚠️ Les fichiers macOS ne se compilent qu'AVEC CGO (l'icône de barre de menus
// passe par Cocoa). Une compilation croisée depuis Windows ou Linux, comme les
// analyseurs lancés sans CGO, ne les voit PAS : un symbole qu'eux seuls
// utilisent passe pour du code mort, et le supprimer casse le build macOS sans
// aucun avertissement local. Seul le job « macos » de la CI le détecte. Avant de
// supprimer un symbole jugé inutilisé, vérifier `grep` sur les fichiers _darwin.
//
// UN SERVICE à l'exécution : ajean-ui (« ajean web ») sert l'UI locale, le
// tunnel du relais et l'endpoint OpenAI depuis un SEUL process, condition d'une
// conversation unique, celle-ci vivant en mémoire (voir chat_conversation.go).
// Les modèles sont des services système à part, qu'AJEAN pilote sans les
// exécuter lui-même (voir services.go et docs/moteurs.md).
//
// ui/index.html est GÉNÉRÉ depuis ui/src/ : éditer les sources puis
// `go generate ./internal/ajean` (voir tools/assemble-ui).
package ajean
