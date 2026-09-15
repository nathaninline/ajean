Binaires précompilés réparés, compilation interruptible, tâches préservées, et corrections d'interface.

## Binaires précompilés à nouveau détectés

llama.cpp a changé la publication de ses releases : celle marquée « latest » ne contient plus de binaires. Le mode précompilé affichait « aucun binaire précompilé adapté à cette machine ». La détection suit désormais les bonnes releases et retrouve le binaire de la machine.

## Arrêter une compilation

Un bouton permet d'interrompre une installation ou une compilation en cours ; les processus sont arrêtés proprement et l'opération peut être relancée.

## Une nouvelle conversation n'interrompt plus une tâche

Ouvrir une nouvelle conversation pendant qu'une tâche planifiée s'exécutait pouvait l'interrompre et perdre son compte-rendu. La tâche continue maintenant jusqu'à son terme.

## Menu AJEAN LINK sur le portail distant

La section AJEAN LINK réapparaît sur le portail d'accès distant, en mode informatif (adresse et état de connexion).

## Interface

- L'indicateur d'activité pendant une compilation ne clignote plus et reste stable ; le même indicateur circulaire est utilisé pour le banc d'essai.
- Un bandeau discret signale, en bas du menu, la disponibilité d'une nouvelle version.
- La vérification de mise à jour est mise en cache pour éviter des erreurs de quota côté GitHub.

## Mise à jour

```
ajean update
```
