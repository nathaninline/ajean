Cette version permet de brancher un moteur d'inférence tiers sur un preset et de le piloter depuis AJEAN comme le moteur local. Elle corrige aussi les appels d'outils en parallèle, le moteur qui redémarrait en boucle sur un preset externe, et la compilation de llama.cpp sur les cartes AMD et Intel.

## Moteurs tiers pilotés par AJEAN

Un preset API externe peut désormais nommer l'unité systemd qui sert son API, avec la clé `EXTERNAL_SERVICE=ajean-<nom>` (Linux). Cas typique : un autre moteur d'inférence installé sur la même machine, qui a besoin des GPU.

- **Bascule** : choisir le preset arrête le moteur llama.cpp et démarre l'unité ; revenir sur un preset local l'arrête et relance llama.cpp. Au démarrage de la machine, l'unité est relancée si son preset est actif.
- **GPU libérés avant de recharger** : llama.cpp pouvait démarrer à la seconde où l'autre moteur s'arrêtait, avant que la carte ait rendu sa mémoire, et échouer faute de VRAM. AJEAN attend désormais que les processus de l'unité arrêtée aient quitté les GPU, sans attendre les autres programmes qui les utilisent.
- **État réel** : la pastille indiquait « prêt » pendant tout le chargement du modèle. Elle affiche « chargement » tant que l'API du moteur ne répond pas, et une erreur si l'unité s'est arrêtée.
- **Benchmark** : le bouton est disponible sur ces presets, et le résultat s'affiche dans la liste des presets comme pour les autres. La mesure se fait en flux (délai du premier token pour la lecture du prompt, puis génération), et chaque essai commence par un texte unique : un moteur qui réutilise sa conversation en cache affichait sinon des milliers de tokens « lus » en 0,1 s.
- **Vitesse sous les réponses** : avec un moteur qui ne renvoie pas les mesures de llama.cpp, la vitesse de génération disparaissait. AJEAN la calcule désormais lui-même. La vitesse de lecture du prompt n'est pas affichée dans ce cas, car le moteur a pu en réutiliser une partie.
- Seules les unités nommées `ajean-*` sont acceptées (le preset est modifiable à distance), et chacune demande sa propre règle sudoers. L'éditeur de presets conserve la clé lors d'une modification.

## Corrections

- **Appels d'outils en parallèle** : quand le modèle demandait deux outils dans la même réponse (lire une page mémoire et un tracker, par exemple), les deux appels étaient fusionnés en un seul, au nom du second, avec des arguments illisibles. Le modèle recevait « JSON invalide » et devait recommencer un par un. Chaque morceau de flux est désormais rangé selon l'appel auquel il appartient. Le problème touchait les serveurs qui envoient un morceau par message (llama.cpp n'était pas concerné).
- **Moteur en redémarrage permanent sur un preset externe** (issue #95) : avec un preset API externe actif, le service moteur échouait sur « BIN non défini » et systemd le relançait toutes les 5 secondes. Il s'arrête désormais proprement, puisqu'aucun moteur local n'est nécessaire. BIN et MODEL reviennent en repassant sur un preset local.

## Compilation de llama.cpp sur GPU AMD et Intel (issue #92)

- **Windows** : un GPU AMD était toujours compilé en CPU, la détection ne cherchant ROCm et Vulkan qu'aux emplacements Linux. Le Vulkan SDK (`winget install KhronosGroup.VulkanSDK`) est désormais reconnu, à condition d'être complet : en-têtes, bibliothèque, compilateur de shaders et pilote Vulkan. À défaut, le comportement reste celui d'avant, car un build GPU raté ne se replie pas sur le CPU.
- **Conseil explicite** : quand la compilation part en CPU alors qu'une carte graphique est présente, l'installation l'indique et donne la marche à suivre (Vulkan pour AMD et Intel, avec la commande adaptée au système ; CUDA Toolkit pour NVIDIA), en ligne de commande comme dans l'interface.
- **`--backend vulkan`** est accepté en plus de `--backend=vulkan` : la forme avec espace était refusée en « option inconnue ». L'option figure maintenant dans `ajean help`.

## Aide et documentation

- `ajean help` liste la commande `computer` (contrôle du navigateur), absente jusqu'ici, et le mode mémoire `search`.
- README (anglais et français) : presets API externe et GPU Cloud, moteurs tiers, choix du backend et voie Vulkan pour AMD et Intel. La section sur l'accès internet, présente seulement en français, est traduite en anglais.

## Mise à jour

    ajean update

Vérifié sur un serveur Linux à deux GPU NVIDIA : bascules répétées entre un moteur tiers et llama.cpp (aucune erreur de mémoire), état « chargement », benchmark et vitesse affichée, appels d'outils en parallèle. Non testé sur du matériel réel, faute de carte AMD ou Intel : la détection du Vulkan SDK sous Windows et le conseil affiché lors d'une compilation en CPU (couverts par des tests automatiques).
