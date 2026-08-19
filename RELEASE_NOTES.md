Cette version ajoute un retour visuel pendant la génération et corrige un défilement bloqué sur mobile.

## Génération

* Pendant que l'IA répond, une ligne d'état s'affiche sous le message : logo AJEAN, temps écoulé qui défile, nombre de tokens produits, puis la vitesse une fois terminé. Sur un long raisonnement ou une exécution d'outil, on voit ainsi que ça travaille au lieu de se demander si ça a planté (issue #34).
* La durée totale et les mesures sont figées sous la réponse et restent identiques après un rechargement de la page.
* La vitesse en tokens par seconde est la vitesse de génération pure : elle exclut le temps de préchauffe et les attentes d'exécution d'outils, et se calcule à partir des horodatages du serveur pour rester stable même à travers l'accès distant.
* Le compteur de tokens affiche bien le total du tour, y compris quand plusieurs outils sont appelés.

## Mobile

* La fin d'une réponse pouvait rester cachée derrière la zone de saisie, sans possibilité de faire défiler pour la lire. En cause : Safari iOS n'inclut pas la marge basse d'un conteneur défilable dans sa hauteur de défilement, donc le fil se croyait entièrement affiché. L'espace sous le fil est désormais réservé par un vrai bloc, toujours pris en compte : on peut à nouveau tout faire défiler.
* Le texte s'efface en fondu derrière la carte de saisie, avec une marge au-dessus pour que la dernière ligne garde de l'air.

## Linux

* L'option CPUAccounting du service systemd, dépréciée puis supprimée dans les versions récentes de systemd, provoquait un avertissement au démarrage. Elle a été retirée (issue #31).

## Mettre à jour

```
ajean update
```

Testé sur le serveur de Alice et sur iPhone (Safari). La mesure de hauteur de la zone de saisie a été rendue plus robuste pour iOS, mais le rendu n'a pas été vérifié sur toutes les tailles d'écran.
