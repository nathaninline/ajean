Correctifs autour du nom de preset dans la ligne d'état.

## Le bon preset conservé pour chaque réponse

La ligne d'état sous une réponse affichait le preset courant, lu au moment du rendu. Après un changement de preset ou un rechargement de la page, les anciennes réponses se retrouvaient donc étiquetées avec le preset du moment, pas celui qui avait réellement répondu. Le preset est désormais enregistré au moment où la réponse est produite, puis rejoué tel quel : chaque réponse garde le nom du preset qui l'a générée.

## Masquer le nom du preset

Un réglage a été ajouté dans Apparence pour retirer le nom du preset de la ligne d'état, pour qui préfère ne garder que la durée, le nombre de tokens et la vitesse. Les autres informations restent affichées.

## Affichage du sélecteur de moteur

Dans l'éditeur de modèle, l'indication d'un moteur non installé se posait sous chaque bouton, ce qui cassait le sélecteur sur deux lignes. Elle tient maintenant sur une seule ligne discrète sous le sélecteur, qui nomme le ou les moteurs à installer.

## Mise à jour

```
ajean update
```
