Cette version rend les tâches planifiées plus autonomes et cohérentes d'une exécution à l'autre, agrandit leur éditeur, corrige leur affichage sur téléphone, et remet les fichiers créés par l'IA à leur place.

## Tâches planifiées

Jusqu'ici, chaque exécution d'une tâche repartait de zéro : l'IA ne savait pas qu'elle tournait toute seule en arrière-plan, et elle ignorait ce qu'elle avait produit la fois d'avant.

* L'IA sait désormais qu'elle est exécutée automatiquement, sans personne en face, et qu'elle doit livrer un compte-rendu clair à la fin.
* Elle reçoit aussi le compte-rendu de son exécution précédente, pour assurer la continuité (par exemple ne ressortir que les nouveautés au lieu de tout répéter). La conversation du chat reste, elle, totalement à l'écart : une tâche ne pollue pas le fil.

## Interface

* La zone de saisie de la consigne était trop à l'étroit dans un petit cadre. Elle est maintenant plus grande et posée à ras, sans marges inutiles autour.

## Mobile

* L'éditeur de tâche s'affichait écrasé sur téléphone, le contenu tassé sur une fraction de la largeur. Il s'ouvre désormais en feuille pleine largeur ancrée en bas, avec des marges resserrées et de la place pour les menus déroulants.
* Dans la liste des tâches, la fréquence, le preset et la date de prochaine exécution s'empilaient sur plusieurs lignes par tâche. Chaque tâche tient maintenant sur une seule ligne, le nom du preset se raccourcissant en premier quand la place manque.

## Fichiers créés par l'IA

* En mode agent, l'IA cherchait parfois à écrire dans des dossiers système comme /usr/local/bin, ce qui échouait faute de droits et la faisait tourner en rond. Elle sait maintenant que son dossier de travail est l'endroit par défaut de tout ce qu'elle crée, et qu'elle ne doit viser un chemin système que si on le lui demande explicitement.

## Mise à jour

Depuis un terminal :

```
ajean update
```

Ou par l'interface, bouton de mise à jour.

Les tâches planifiées ont été vérifiées de bout en bout (exécution, continuité, affichage) ; le comportement macOS n'a pas été retesté sur cette version.
