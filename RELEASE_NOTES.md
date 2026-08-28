Postes distants plus stables, agent moins bridé, affichage de la génération plus lisible, et prise en charge GPU AMD améliorée.

## Postes distants

* Fin des déconnexions à répétition. Un ping WebSocket périodique maintient désormais la liaison entre un poste et le serveur, des deux côtés. Sans lui, une liaison inactive était coupée au bout d'une minute ou deux par les intermédiaires réseau (relais, proxy), puis reconnectée en boucle. Le symptôme « le poste se coupe tout seul, puis revient » disparaît.

## Agent

* Suppression du garde-fou qui refusait les commandes destructrices visant les dossiers internes. L'assistant peut de nouveau supprimer, déplacer et renommer des fichiers librement. La durabilité des scripts conservés repose sur leur séparation d'avec le dossier de travail jetable, pas sur un filtre de commandes. Le dossier de mémoire reste, lui, accessible uniquement par ses outils dédiés (il est chiffré).
* La même commande peut être relancée à l'identique dans un même échange. Un appel de terminal répété n'est plus court-circuité par un « déjà fait » : relancer un fichier après l'avoir modifié fonctionne comme attendu.

## Affichage de la génération

* L'interface reste réactive pendant que l'assistant répond. Le défilement automatique était réécrit à chaque image, ce qui, sur l'application installée (PWA), pouvait empêcher tout appui à l'écran tant que la réponse n'était pas terminée. Le défilement est désormais espacé pour laisser passer les interactions.
* Le compteur de la ligne d'état inclut maintenant les jetons produits pour écrire le contenu d'un outil (le code écrit dans un fichier, une commande de terminal), qui n'étaient comptés nulle part.
* Compteur de jetons abrégé au dela de mille (1K, 1.1K, 12.3K) et durée au format compact (5m 25s, 1h 05m).
* Après un rafraîchissement en pleine génération, un bloc de raisonnement en cours reste affiché « en cours » et animé, au lieu de se figer sur son décompte final.

## GPU AMD

* La carte GPU / VRAM de l'interface reconnait aussi les installations ROCm qui ne disposent que de `rocm-smi` (et pas de `amd-smi`) : le GPU AMD apparait au lieu d'un « pas de GPU » trompeur alors que l'inference tourne dessus.
* La commande `ajean gpu` indique clairement, sur une machine AMD, que le choix de carte se règle dans l'interface (via le moteur), et non par cette commande réservée aux cartes NVIDIA.

## Mettre à jour

```
ajean update
```
