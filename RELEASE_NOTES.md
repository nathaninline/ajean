Jean devient **AJEAN**, partout et plus seulement dans l'interface. Le dépôt, la commande, les fichiers, les dossiers et les services portent désormais le même nom.

Cette version a été construite autour d'une contrainte simple : **rien de ce qui fonctionne aujourd'hui ne doit cesser de fonctionner**. Il n'y a rien à réinstaller et rien à reconfigurer.

## La commande `jean` continue de marcher

`ajean` devient la commande principale, mais `jean` reste installée à côté et fait exactement la même chose. Les alias, les scripts, les tâches planifiées et les raccourcis existants ne bougent pas.

De même, `JEAN_HOME` reste lu s'il a été défini à la main, au même titre que le nouveau `AJEAN_HOME`. Un emplacement choisi explicitement est un choix, pas un héritage à corriger.

## Le dossier de données se renomme tout seul

Au premier lancement, `%ProgramData%\jean` devient `%ProgramData%\ajean` (`/etc/jean` → `/etc/ajean` sous Linux et macOS).

Le déplacement est instantané, même avec 200 Go de modèles : rien n'est recopié, le dossier est renommé sur place. Les chemins absolus qui pointaient dedans — dont le `--chat-template-file` des presets — sont mis à jour dans la foulée.

Si le renommage ne peut pas aboutir, par exemple parce qu'AJEAN tourne encore en tâche de fond, **il ne se passe rien du tout** : l'ancien dossier reste en place et continue de servir. Rien n'est jamais copié ni supprimé, et la tentative est reprise au lancement suivant.

## Les serveurs Linux ne perdent pas la main

Une mise à jour remplace le binaire, pas les unités systemd. AJEAN utilise donc le service réellement installé sur la machine : un serveur qui tourne sous `jean.service` continue sous `jean.service`.

Sans cette précaution, le premier redémarrage après mise à jour aurait échoué — sur une machine distante, cela signifie l'accès coupé sans moyen simple de le rétablir.

## Mise à jour

```
ajean update
```

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule. Les installations existantes se mettent donc à jour normalement, sans intervention.

Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail : le fichier détecte l'installation existante et propose de la remplacer.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
