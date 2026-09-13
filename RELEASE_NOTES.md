Version de corrections, plus un nouveau réglage de mémoire par projet.

## Mémoire

Le mode mémoire se règle désormais **par projet**, avec quatre choix dans la fenêtre Mémoire :

- **auto, injecté** : l'index des pages est placé en tête de conversation, l'IA voit d'emblée ce qu'elle sait. C'est le comportement actuel, resté le réglage par défaut.
- **auto, recherche** : rien n'est injecté, l'IA cherche dans sa mémoire avant chaque tâche. Le contexte reste léger et le modèle démarre plus vite. C'est le retour du fonctionnement d'avant la version 0.13.0.
- **sur demande** : les outils mémoire existent mais ne servent que si on le demande.
- **désactivée** : aucun accès mémoire.

Chaque projet garde son propre réglage. Les projets existants conservent leur comportement (index injecté).

## Corrections

- **Images tournées** : les photos venant d'un téléphone (orientation stockée en métadonnées EXIF) arrivaient au modèle tournées de 90 degrés, qui annonçait devoir les redresser mentalement. L'orientation est maintenant appliquée aux pixels avant l'envoi, uniquement pour les JPEG concernés, avec repli sur l'image d'origine en cas de souci.
- **Mode agent désactivé** : en chat pur, le modèle pouvait quand même tenter d'appeler des outils qui n'existent pas dans ce mode, ce qui partait parfois en boucle. Ces appels sont désormais ignorés quand aucun outil n'est proposé.
- **Réordonnancement des presets** : après avoir déplacé un preset par glissement, cliquer dessus tout de suite sélectionnait le preset qui occupait l'ancienne position. La liste est maintenant réactualisée une fois le nouvel ordre enregistré.

## Interface

- Le message sous la zone de chat n'affiche plus « le modèle charge » quand aucun modèle n'est chargé : il distingue le chargement en cours de l'absence de modèle.

## Mise à jour

```
ajean update
```

Non vérifié sur cette version : le rendu de l'orientation EXIF n'a pas été testé sur un vrai flux multimodal (mmproj) avec une photo de téléphone, seulement en tests unitaires.
