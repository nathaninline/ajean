Version corrective. Deux réglages de l'interface qui ne se comportaient pas comme prévu à la création d'un preset et à la saisie d'un message.

## Presets : l'interrupteur Raisonnement écrit désormais sa valeur

* À la création d'un preset, laisser l'interrupteur Raisonnement sur off n'écrivait aucune valeur dans la configuration. Or une valeur absente laisse le moteur suivre son défaut, et un modèle à raisonnement continue alors de réfléchir malgré l'interrupteur affiché sur off. Il fallait basculer l'interrupteur deux fois pour forcer l'écriture.
* Désormais l'état de l'interrupteur est enregistré tel quel au moment de la sauvegarde : off inscrit une désactivation explicite, on l'active. Une valeur plus précise déjà en place (auto, deepseek) est préservée.

## Saisie : option pour inverser Entrée et Maj+Entrée

* Nouvelle option dans Apparence : **Entrée = retour à la ligne**. Désactivée par défaut, le comportement habituel ne change pas (Entrée envoie, Maj+Entrée va à la ligne).
* Une fois activée, la logique est inversée : Entrée insère un retour à la ligne, et l'envoi se fait avec Maj+Entrée ou Ctrl+Entrée. Le réglage est partagé entre les appareils reliés au même serveur.

> Aucun rechargement de modèle à la mise à jour, le moteur d'inférence n'est pas touché. Le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
