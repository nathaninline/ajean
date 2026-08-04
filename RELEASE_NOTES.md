## Le bouton « mettre à jour » redémarre AJEAN tout seul

Sous Windows, il fallait jusqu'ici quitter puis relancer l'application à la main pour que la mise à jour prenne effet. C'est fait automatiquement : la page se reconnecte d'elle-même au bout de quelques secondes.

Ce redémarrage règle aussi, au passage, le renommage du dossier de données. Tant qu'AJEAN tourne, il tient son propre dossier, et `C:\ProgramData\jean` ne peut pas devenir `C:\ProgramData\ajean` — c'est ce qui laissait certains postes indéfiniment sur l'ancien nom malgré les versions précédentes. Le redémarrage ouvre le court instant où plus rien ne le tient, et la migration s'y termine.

Le raccourci du menu Démarrer et la commande `ajean` suivent le déplacement, puisque le programme installé vit à l'intérieur de ce dossier.

Si quelque chose empêche le renommage, il ne se passe rien : AJEAN redémarre sur son dossier actuel et réessaiera plus tard. Rien n'est jamais copié ni supprimé.

## Mise à jour

```
ajean update
```

Ou le bouton de l'interface, qui se charge désormais de tout.

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule.
