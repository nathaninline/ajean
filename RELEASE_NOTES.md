Le niveau de réflexion se règle maintenant en un clic depuis la barre de saisie, sans rouvrir l'éditeur de preset ni redémarrer le moteur.

## Interface

* Nouveau raccourci **« niveau de réflexion »** dans la barre du composeur, à côté du bouton des postes distants : une petite jauge à barres montre le niveau du preset actif (low, medium, high, xhigh). Le nombre de barres allumées correspond au niveau, tu le vois donc d'un coup d'oeil, sans cliquer. Un clic ouvre un menu pour en changer, le niveau courant y est coché.
* Le changement est pris en compte dès le message suivant, sans redémarrer le moteur. Le raccourci n'apparaît que sur un preset qui définit déjà un effort de réflexion (il n'a d'effet qu'avec `--jinja`, on ne le propose donc que là où il compte).
* Le décompte de contexte au bas de la carte est abrégé (par exemple `10.2K / 40K`) pour gagner de la place. Les valeurs exactes et le pourcentage restent dans l'infobulle.

## Corrections

* Changer le niveau de réflexion ne désélectionnait plus le preset actif : l'état « actif » était déduit en comparant la configuration vivante au fichier du preset, et écrire le niveau les faisait diverger. Le preset chargé est désormais mémorisé explicitement, il reste donc coché quand tu ajustes le niveau à chaud. La bascule se répare toute seule au premier chargement, sans devoir re-sélectionner le preset après la mise à jour.

> Note : version purement interface, le moteur d'inférence n'a pas été touché (aucun rechargement de modèle à la mise à jour). Vérifiée sur le serveur de Alice (Linux) ; le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
