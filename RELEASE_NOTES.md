Détection des GPU AMD et Intel sous Windows dans le panneau Appareil, synchronisation du preset actif entre appareils sans rechargement, et plusieurs corrections de fiabilité au démarrage et pendant la génération.

## Nouveautés

* **GPU AMD et Intel visibles sous Windows.** Sur une machine Windows sans NVIDIA, le panneau Appareil affichait « pas de GPU » alors que l'inférence tournait bien via Vulkan, faute d'outil de mesure disponible (nvidia-smi, amd-smi et rocm-smi sont tous absents dans ce cas). La carte affiche désormais chaque GPU vu par le moteur : nom, VRAM totale et utilisée, pourcentage d'utilisation, et température pour les cartes AMD. Sans effet sur les configurations NVIDIA, déjà couvertes.

## Corrections

* La conversation active pouvait revenir vide après un redémarrage du service, alors qu'elle existait toujours (elle restait récupérable dans l'historique). Une lecture de la base momentanément indisponible au démarrage était confondue avec une absence de données. La lecture est maintenant distinguée d'une absence réelle, réessayée, et l'erreur éventuelle est journalisée au lieu de disparaître en silence.
* Changer de preset ou de modèle sur un appareil ne se reflétait pas sur les autres tant que l'application n'était pas rechargée. Le preset actif se resynchronise à présent automatiquement sur tous les appareils connectés.
* Lorsque le modèle raisonnait sans jamais appeler d'outil ni répondre, une seule relance automatique ne suffisait pas toujours et l'utilisateur voyait un tour vide. Une seconde relance, plus directe, intervient désormais avant d'abandonner.
* Sur un tour au raisonnement très long, le début de la conversation pouvait être tronqué du flux de relecture (sans perte dans la conversation enregistrée). La marge de ce journal d'affichage a été relevée en conséquence.

## Mise à jour

```
ajean update
```
