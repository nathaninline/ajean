Le renommage est terminé. Plus rien ne s'appelle jean : ni le binaire, ni les services, ni les variables, ni les dossiers. Et le dossier de données, qui s'était couvert d'une douzaine de petits fichiers d'état, tient désormais dans six dossiers et une base.

## Mise à jour : il faut réinstaller

**Ne passez pas par le bouton « mettre à jour » de la 0.7.** Il ne trouvera d'ailleurs rien : les binaires de cette version portent de nouveaux noms, que la 0.7 ne sait pas chercher. C'est délibéré. La laisser installer ce binaire aurait remplacé l'exécutable sans migrer ni les données ni les unités : le service de lien serait reparti en boucle d'échec sur une sous-commande disparue, et le moteur n'aurait plus trouvé sa configuration. Une machine à réparer en SSH après un clic dans un navigateur.

La marche à suivre, sur une machine déjà installée :

```bash
curl -L -o ajean https://github.com/nathaninline/ajean/releases/latest/download/ajean-linux
chmod +x ajean && sudo mv ajean /usr/local/bin/ajean
sudo ajean install
```

`install` fait la reprise complète : il arrête et désactive les anciens services, déplace `configs/` vers `presets/`, `MEMORY/` vers `memory/` et les `.gguf` vers `models/`, reprend en base la configuration, les préférences, la conversation, les clés, le jeton de liaison, les interrupteurs, les serveurs MCP et les benchmarks, puis installe les deux nouvelles unités.

Rien n'est supprimé. Les presets, la mémoire et les modèles sont déplacés, jamais copiés ni effacés. Les anciens fichiers d'état sont rangés dans `avant-0.8/`, que vous pourrez supprimer quand tout ira bien. La clé du chiffrement de bout en bout n'est pas touchée, donc l'empreinte confirmée dans le portail reste valable.

## Une base à la place des fichiers d'état

`config.env`, `webprefs.json`, `conversation.json`, `model_dirs.json`, `mcp.json`, `.api_key`, `.web_key`, `.link_token`, `.agent_enabled`, `.internet_enabled` et le reste ont disparu. Tout cela vit maintenant dans **`ajean.db`**, un fichier unique en [bbolt](https://github.com/etcd-io/bbolt), pur Go, transactionnel, sans dépendance système.

Ce n'est pas qu'un rangement. Chaque fichier avait sa façon d'être écrit, et donc sa façon de rater une écriture concurrente : l'interface et un tour de chat qui touchaient au même réglage au même instant pouvaient en perdre un. Une transaction remplace tout ça. Une bascule de preset, en particulier, remplace la configuration d'un bloc : il n'existe plus d'instant où elle serait à moitié l'ancienne et à moitié la nouvelle.

Restent des fichiers ceux qui sont faits pour être lus, édités et sauvegardés à la main : les presets, les pages de mémoire, les modèles. `ajean edit` déroule donc la configuration au format `clé=valeur` dans votre éditeur, puis la relit ; le fichier n'existe que le temps de l'édition.

## Six dossiers

`$AJEAN_HOME` contient `backends/`, `bin/`, `presets/`, `memory/`, `models/`, `workspace/`, plus la base. `configs/` devient `presets/`, `MEMORY/` devient `memory/`, et les `.gguf` ont enfin leur `models/` au lieu d'être posés à la racine. Ne restent à côté que ce qui ne peut pas aller ailleurs : la clé privée du chiffrement de bout en bout, le dossier des certificats TLS, les journaux et les fichiers PID des services.

## Deux services qui portent enfin leur nom

`ajean.service` et `ajean-link.service` deviennent **`ajean-engine`** et **`ajean-ui`**. Le premier exécute le modèle, le second sert l'interface web, le tunnel d'accès distant et l'endpoint OpenAI. Le nom « link » cachait l'essentiel : ce service est d'abord le serveur web.

Surtout, il n'y a plus **qu'une seule façon** de servir l'interface. Avant, `ajean web` et `ajean link serve` savaient tous deux le faire, ce qui posait un piège permanent : lancer les deux, c'était un conflit sur le port 8090 et surtout deux fils de conversation qui divergeaient (la conversation vit en mémoire, deux process qui la servent finissent par s'écraser l'un l'autre). La consigne « ne jamais lancer `ajean web` » circulait comme une règle à retenir ; elle n'existait que parce que le code offrait deux portes pour la même pièce.

Désormais `ajean web` est cette porte unique : il sert l'interface et, si un jeton de liaison est enregistré, ouvre le tunnel dans le même process. `ajean link` ne s'occupe plus que du compte (jeton, code d'appairage, état), et `ajean ui start|stop|restart|status` pilote le service. `ajean link serve`, `link start`, `link stop` et `link restart` disparaissent.

Séparer les deux services garde son intérêt : redémarrer l'interface est instantané, alors que redémarrer le moteur recharge des dizaines de gigaoctets.

## Sur Windows

**« Quitter » arrête vraiment tout.** Le moteur tourne dans un processus détaché, qui survit volontairement à la fermeture de l'interface pour garder le modèle chargé entre deux ouvertures. Mais après un « Quitter » depuis la zone de notification, plus rien ne le pilotait et il conservait des dizaines de gigaoctets de mémoire, sans la moindre fenêtre pour l'expliquer. Quitter décharge désormais le modèle. Ailleurs, rien ne change : sous Linux et macOS le moteur appartient à systemd ou launchd, et fermer une interface n'a pas à arrêter un service système.

**La redirection et les tubes fonctionnent.** `ajean where > fichier.txt` n'écrivait rien dans le fichier, et `ajean help | findstr quelquechose` ne transmettait rien : tout partait sur la console. Le programme réouvrait sa sortie sur la console pour avoir où écrire, et écrasait ce faisant ce que le shell lui avait branché.

À savoir : lancé depuis `cmd`, AJEAN rend la main immédiatement et son affichage arrive après la nouvelle invite. C'est le prix du sous-système graphique, celui-là même qui évite la fenêtre noire au double-clic.

**Plus de question au premier lancement.** Le double-clic demandait s'il fallait installer AJEAN ou seulement le lancer. La question n'avait qu'une réponse utile : rester à l'emplacement du fichier téléchargé ne donne pas une installation exploitable, sans raccourci, sans rien dans le PATH, et avec une application qui disparaît le jour où l'on vide ses téléchargements. L'installation se fait donc directement, et le message qui suit dit ce qui a été fait au lieu de demander une permission.

## Ce qui a été retiré

Tout le code écrit pour ménager les installations « jean » : la migration du dossier de données et ses reprises après échec, la migration de l'agencement système (unités, `/etc/default`, réécriture des chemins), l'élévation Windows qu'elle demandait, la résolution du nom d'unité réellement installée, la reprise des fichiers PID et des skills, les alias `jean` posés à l'installation, les variables `JEAN_*` lues en second.

La CLI perd ses alias hérités (`skills`, `machine`, `tools`, `web-access`, `mem`, `upgrade`, `self-update`, `paths`, `llama`) et son aide est réorganisée autour des deux services. Chaque commande a désormais un seul nom. `app` quitte l'aide : c'est le comportement du double-clic, qu'on n'atteint pas en tapant son nom.

Les binaires publiés prennent des noms lisibles : `ajean-linux`, `ajean-linux-arm`, `ajean-macos`, `ajean-macos-arm`, `ajean-windows.exe`, `ajean-windows-arm.exe`. Le suffixe `-arm` désigne l'arm64, son absence l'x86-64.

## Ce qui n'a pas été testé

La reprise 0.7 vers 0.8 a été vérifiée sur Linux (un serveur réel, avec 9 presets, 24 pages de mémoire et 8 modèles) et sur Windows (dossier de test complet), plus par trois tests automatisés. **Elle n'a pas été essayée sur macOS**, faute de machine : le support macOS reste globalement non validé sur du matériel Apple. Sauvegardez `$AJEAN_HOME` avant de vous lancer.
