Jean devient **AJEAN**, partout et plus seulement dans l'interface. Le dépôt, la commande, les fichiers, les dossiers et les services portent désormais le même nom.

Cette version a été construite autour d'une contrainte simple : **rien de ce qui fonctionne aujourd'hui ne doit cesser de fonctionner**. Il n'y a rien à réinstaller et rien à reconfigurer.

## La commande `jean` continue de marcher

`ajean` devient la commande principale, mais `jean` reste installée à côté et fait exactement la même chose. Les alias, les scripts, les tâches planifiées et les raccourcis existants ne bougent pas.

De même, `JEAN_HOME` reste lu s'il a été défini à la main, au même titre que le nouveau `AJEAN_HOME`. Un emplacement choisi explicitement est un choix, pas un héritage à corriger.

## Le dossier de données se renomme tout seul

`%ProgramData%\jean` devient `%ProgramData%\ajean`, et `/etc/jean` devient `/etc/ajean` sous Linux et macOS. Vous n'avez aucune commande à lancer : la migration se fait pendant la mise à jour.

Le déplacement est instantané, même avec 200 Go de modèles — rien n'est recopié, le dossier est renommé sur place. Tout ce qui pointait dedans suit : les chemins absolus de `config.env`, ceux de vos presets (dont le `--chat-template-file`), le fichier `/etc/default`, et le répertoire de travail de l'unité systemd.

Votre clé d'accès distant est déplacée avec le reste : **aucun ré-appairage**, le tunnel se reconnecte seul.

**Si vous aviez choisi vous-même l'emplacement** — un `JEAN_HOME` sur un autre disque, par exemple — il n'est pas touché. Un chemin défini à la main est une décision, pas un héritage à corriger.

Et si le renommage ne peut pas aboutir, par exemple parce qu'AJEAN tourne encore en tâche de fond sous Windows, **il ne se passe rien du tout** : l'ancien dossier reste en place et continue de servir, la tentative sera reprise plus tard. Rien n'est jamais copié ni supprimé.

## Les serveurs Linux ne perdent pas la main

Les unités systemd **ne sont pas renommées**. Un serveur qui tourne sous `jean.service` continue sous `jean.service` : vos commandes `systemctl`, vos règles `sudoers` et vos scripts de supervision restent valables. Seuls les chemins qu'elles contiennent sont corrigés, et une copie de l'unité d'origine est conservée à côté.

Une sauvegarde de la configuration, des presets et de la mémoire est déposée dans `/root` avant toute modification. En cas d'échec à n'importe quelle étape, la machine est remise dans son état initial et les services relancés.

## Mise à jour

```
ajean update
```

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule. Les installations existantes se mettent donc à jour normalement, sans intervention.

Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail : le fichier détecte l'installation existante et propose de la remplacer.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
