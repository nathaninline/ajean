Binaires précompilés réparés, et améliorations autour de la compilation.

## Binaires précompilés à nouveau détectés

llama.cpp a changé la façon dont ses releases sont publiées : la release marquée « latest » ne contient plus de binaires, et les vrais builds sont désormais publiés séparément. Résultat, le mode précompilé affichait « aucun binaire précompilé adapté à cette machine ». La détection suit maintenant les bonnes releases et retrouve le binaire correspondant à la machine.

## Arrêter une compilation

Un bouton permet d'interrompre une installation ou une compilation en cours ; les processus lancés sont arrêtés proprement et l'opération peut être relancée plus tard.

## Indicateur d'activité stable

Pendant une installation, l'indicateur d'activité ne clignote plus : il tourne de façon continue et seule la ligne d'état se met à jour. Le même indicateur circulaire est utilisé pour le banc d'essai.

## Nouvelle version signalée

Un bandeau discret apparaît en bas du menu lorsqu'une nouvelle version d'AJEAN est disponible.

## Mise à jour

```
ajean update
```
