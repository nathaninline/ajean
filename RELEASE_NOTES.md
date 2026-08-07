Le renommage est terminé. Plus rien ne s'appelle jean : ni le binaire, ni les services, ni les variables, ni les dossiers. Et le dossier de données, qui s'était couvert d'une douzaine de petits fichiers d'état, tient désormais dans six dossiers et une base.

**Cette version ne reprend pas les installations précédentes.** Il n'y a aucune migration : ni des dossiers, ni des unités système, ni des réglages. Réinstallez, puis retéléchargez vos modèles ou repointez-les avec *Dossiers de modèles*.

## Une base à la place des fichiers d'état

`config.env`, `webprefs.json`, `conversation.json`, `model_dirs.json`, `mcp.json`, `.api_key`, `.web_key`, `.link_token`, `.agent_enabled`, `.internet_enabled` et le reste ont disparu. Tout cela vit maintenant dans **`ajean.db`**, un fichier unique en [bbolt](https://github.com/etcd-io/bbolt) — pur Go, transactionnel, sans dépendance système.

Ce n'est pas qu'un rangement. Chaque fichier avait sa façon d'être écrit, et donc sa façon de rater une écriture concurrente : l'interface et un tour de chat qui touchaient au même réglage au même instant pouvaient en perdre un. Une transaction remplace tout ça. Une bascule de preset, en particulier, remplace la configuration d'un bloc : il n'existe plus d'instant où elle serait à moitié l'ancienne et à moitié la nouvelle.

Restent des fichiers ceux qui sont faits pour être lus, édités et sauvegardés à la main : les presets, les pages de mémoire, les modèles. `ajean edit` déroule donc la configuration au format `clé=valeur` dans votre éditeur, puis la relit — le fichier n'existe que le temps de l'édition.

## Six dossiers, et rien d'autre

`$AJEAN_HOME` contient exactement `backends/`, `bin/`, `presets/`, `memory/`, `models/`, `workspace/`, plus la base. `configs/` devient `presets/`, `MEMORY/` devient `memory/`, et les `.gguf` ont enfin leur `models/` au lieu d'être posés à la racine.

## Deux services qui portent enfin leur nom

`ajean.service` et `ajean-link.service` deviennent **`ajean-engine`** et **`ajean-ui`**. Le premier exécute le modèle, le second sert l'interface web, le tunnel d'accès distant et l'endpoint OpenAI. Le nom « link » cachait l'essentiel : ce service est d'abord le serveur web.

Surtout, il n'y a plus **qu'une seule façon** de servir l'interface. Avant, `ajean web` et `ajean link serve` savaient tous deux le faire, ce qui posait un piège permanent : lancer les deux, c'était un conflit sur le port 8090 et surtout deux fils de conversation qui divergeaient — la conversation vit en mémoire, deux process qui la servent finissent par s'écraser l'un l'autre. La consigne « ne jamais lancer `ajean web` » circulait comme une règle à retenir ; elle n'existait que parce que le code offrait deux portes pour la même pièce.

Désormais `ajean web` est cette porte unique : il sert l'interface et, si un jeton de liaison est enregistré, ouvre le tunnel dans le même process. `ajean link` ne s'occupe plus que du compte — jeton, code d'appairage, état — et `ajean ui start|stop|restart|status` pilote le service. `ajean link serve`, `link start`, `link stop` et `link restart` disparaissent.

Séparer les deux services garde son intérêt : redémarrer l'interface est instantané, alors que redémarrer le moteur recharge des dizaines de gigaoctets.

## Ce qui a été retiré

Tout le code écrit pour ménager les installations « jean » : la migration du dossier de données et ses reprises après échec, la migration de l'agencement système (unités, `/etc/default`, réécriture des chemins), l'élévation Windows qu'elle demandait, la résolution du nom d'unité réellement installée, la reprise des fichiers PID et des skills, les alias `jean` posés à l'installation, les variables `JEAN_*` lues en second.

La CLI perd ses alias hérités — `skills`, `machine`, `tools`, `web-access`, `mem`, `upgrade`, `self-update`, `paths`, `llama` — et son aide est réorganisée autour des deux services. Chaque commande a désormais un seul nom. `app` quitte l'aide : c'est le comportement du double-clic, qu'on n'atteint pas en tapant son nom.

Les releases ne publient plus qu'un jeu de binaires, `ajean-<os>-<arch>`. La double publication qui accompagnait la transition n'a plus d'objet.
