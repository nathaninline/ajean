## Le renommage du dossier se fait maintenant à la mise à jour

Les versions 0.7.1 à 0.7.3 n'y parvenaient pas sur les postes Windows sans droits administrateur : le code prévu pour demander l'autorisation n'était jamais atteint. Il ne se déclenchait qu'en relançant l'installation ou en retéléchargeant le programme — deux gestes que personne ne fait pour une mise à jour.

`ajean update`, et le bouton de mise à jour de l'interface, s'en chargent désormais. L'autorisation Windows est demandée à ce moment-là, une fois, et le dossier `C:\ProgramData\jean` devient `C:\ProgramData\ajean`.

Le raccourci du menu Démarrer et la commande `ajean` sont repointés dans la foulée. Le programme installé vit à l'intérieur du dossier de données : sans cette étape, le déplacement aurait laissé un raccourci mort et une commande introuvable.

Refuser l'autorisation reste sans conséquence : AJEAN continue sur son dossier actuel, et la question reviendra à la prochaine mise à jour.

Si vous êtes déjà sur la dernière version et toujours sur l'ancien dossier, la migration se fera à la mise à jour suivante.

## Mise à jour

```
ajean update
```

Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail.

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule.
