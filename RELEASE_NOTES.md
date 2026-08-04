## Correction

**Sous Windows, « Une version plus récente d'AJEAN est déjà installée » pouvait s'afficher à chaque lancement.** Le message était exact, mais rien ne permettait d'en sortir : il revenait indéfiniment.

Deux causes, qui se renforçaient.

La commande `jean`, conservée en alias depuis la 0.7.0, est une copie du programme posée à côté de `ajean.exe`. Windows refusant d'écraser un exécutable en cours d'exécution, cette copie n'était pas remplacée si elle tournait au moment de la mise à jour — elle restait figée sur une version périmée. Elle est désormais mise de côté puis réécrite, la même technique que celle utilisée pour le programme principal.

Et les raccourcis du menu Démarrer et du Bureau, créés avant la 0.7.0, visaient toujours cet alias. Ils n'étaient jamais repointés. Chaque lancement exécutait donc l'ancienne version, qui constatait à juste titre qu'une plus récente était installée — et le disait. Un raccourci existant est maintenant repointé vers le programme courant.

Après cette mise à jour, le message disparaît de lui-même au premier lancement.

## Mise à jour

```
ajean update
```

Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail.

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule.
