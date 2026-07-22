// Jean — cœur du binaire (package jean, appelé par cmd/jean). Les fichiers sont préfixés par domaine
// (Go n'autorise pas de sous-dossiers dans un même package) :
//
//	main.go       point d'entrée : dispatch des sous-commandes, chemins (JeanHome…)
//	cli_*         expérience « application » (double-clic : UI + tray + splash)
//	web_*         serveur HTTP local :8090 (UI embarquée via go:embed ui/, auth, prefs)
//	chat_*        chat CLI + conversation serveur partagée, compaction, mémoire,
//	              outils de l'agent (dont accès internet via Crawl4AI)
//	llm_*         client llama-server (complétions, endpoint OpenAI, bench, test)
//	backend_*     gestion llama.cpp : build (llamacpp), GPU, serve (ExecStart),
//	              modèles/téléchargements, catalogue, presets, config.env
//	relay_*       accès distant ajean.link : tunnel (link), chiffrement E2E, appairage
//	sys_*         intégration OS : install, services (systemd/launchd/Windows),
//	              plateforme, process, tray, splash, tty, auto-update
//
// Les suffixes _windows/_linux/_darwin/_unix/_other portent les contraintes de
// compilation par OS. ui/index.html est GÉNÉRÉ depuis ui/src/ (voir ui/assemble.ps1).
package jean
