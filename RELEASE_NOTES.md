Suite à des retours utilisateurs, le prompt système est restauré tel qu'il était avant la v0.12.4.

## Prompt système restauré

* La v0.12.4 avait reformulé le prompt système pour rendre le raisonnement plus court et plus actif (réfléchir brièvement, annoncer l'action, agir, vérifier, continuer). Sur certains modèles, cette formulation favorisait des enchaînements d'actions qui tournaient en boucle.
* Le prompt revient donc exactement à sa version d'avant la v0.12.4, avec la consigne « agir immédiatement, ne jamais terminer un tour après avoir seulement réfléchi, rester concis ».
* Ce retour ne supprime pas à lui seul toute possibilité de boucle sur un modèle qui ignore ses consignes, mais il rétablit le comportement connu comme stable.

## Mettre à jour

```
ajean update
```
