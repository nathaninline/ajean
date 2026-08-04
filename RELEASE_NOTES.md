Jean devient **AJEAN**, partout et plus seulement dans l'interface. Le dépôt, la commande, les dossiers et les services portent désormais le même nom.

Cette version a été construite autour d'une contrainte simple : **rien de ce qui fonctionne aujourd'hui ne doit cesser de fonctionner**. Il n'y a rien à réinstaller, rien à reconfigurer, et rien à déplacer soi-même.

## La commande `jean` continue de marcher

`ajean` devient la commande principale, mais `jean` reste installée à côté et fait exactement la même chose. Vos alias, vos scripts, vos tâches planifiées et vos raccourcis ne bougent pas.

De même, `JEAN_HOME` reste lu s'il a été défini à la main, au même titre que le nouveau `AJEAN_HOME`. Un emplacement choisi explicitement est une décision, pas un héritage à corriger.

## Le dossier de données se renomme tout seul

`%ProgramData%\jean` devient `%ProgramData%\ajean`, et `/etc/jean` devient `/etc/ajean` sous Linux et macOS. Aucune commande à lancer : la migration se fait pendant la mise à jour.

Le déplacement est instantané, même avec 200 Go de modèles — rien n'est recopié, le dossier est renommé sur place. Tout ce qui pointait dedans suit : les chemins de `config.env`, ceux de vos presets (dont le `--chat-template-file`), et jusqu'au raccourci du menu Démarrer, puisque le programme installé vit à l'intérieur de ce dossier.

Votre clé d'accès distant est déplacée avec le reste : **aucun ré-appairage**, le tunnel se reconnecte seul.

Si vous aviez choisi vous-même l'emplacement, il n'est pas touché. Et si le renommage ne peut pas aboutir, **il ne se passe rien du tout** : l'ancien dossier reste en place et continue de servir, la tentative sera reprise plus tard. Rien n'est jamais copié ni supprimé.

## Le bouton de mise à jour redémarre AJEAN

Sous Windows, il fallait quitter puis relancer l'application à la main pour qu'une mise à jour prenne effet. C'est fait automatiquement, et la page se reconnecte d'elle-même.

Ce redémarrage est aussi ce qui permet au renommage d'aboutir : tant qu'AJEAN tourne, il tient son propre dossier. Le redémarrage ouvre le court instant où plus rien ne le retient.

## Les serveurs Linux ne perdent pas la main

Les unités systemd **ne sont pas renommées**. Un serveur qui tourne sous `jean.service` continue sous `jean.service` : vos commandes `systemctl`, vos règles `sudoers` et vos scripts de supervision restent valables. Seuls les chemins qu'elles contiennent sont corrigés, et une copie de l'unité d'origine est conservée à côté.

Une sauvegarde de la configuration, des presets et de la mémoire est déposée dans `/root` avant toute modification. Si une étape échoue, la machine est remise dans son état initial et les services relancés.

## Plus de dossier SKILLS

Les skills avaient été fondus dans la mémoire de l'IA, mais un dossier `SKILLS` vide continuait d'être créé. Il ne l'est plus. Si vous en avez encore un, son contenu est repris dans la mémoire et vous pouvez ensuite l'effacer.

## Mise à jour

```
ajean update
```

Ou le bouton de l'interface, qui se charge désormais de tout.

Les binaires sont publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que les installations existantes basculent. Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
