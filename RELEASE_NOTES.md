Cette version répare une série de problèmes autour des moteurs llama.cpp : un moteur mis à jour qui ne l'était pas vraiment, des presets qui perdaient leur moteur, une compilation qui échouait alors que tout était installé. Elle ajoute aussi le réglage des cartes graphiques directement dans l'éditeur de modèle.

## Le moteur précompilé se met vraiment à jour

Chaque mise à jour du moteur précompilé s'installait dans un dossier portant son numéro de version, sans jamais supprimer les précédents. AJEAN retenait ensuite le premier dossier trouvé par ordre alphabétique, c'est à dire le plus ANCIEN. Sur une machine mise à jour plusieurs fois, le moteur réellement utilisé pouvait donc être une vieille version, parfois une installation incomplète qui refusait de démarrer avec une erreur de bibliothèque manquante.

Le moteur retenu est maintenant celui de la version installée, et les anciennes extractions sont supprimées après une mise à jour réussie (plusieurs centaines de mégaoctets récupérés à chaque fois).

## Vos presets ne perdent plus leur moteur

Comme le chemin du moteur précompilé contient son numéro de version, il changeait à chaque mise à jour. Tous les presets qui l'utilisaient basculaient alors en mode « personnalisé », et il fallait les repointer un par un. Un preset dont le moteur a été mis à jour est désormais reconnu, et il démarre même si son chemin exact a disparu : il suit la version installée au lieu d'échouer.

## Compilation d'un moteur : le CUDA Toolkit est enfin trouvé

Sur les machines où `nvcc` vient du paquet de la distribution (`/usr/bin/nvcc`) alors que le toolkit complet vit ailleurs (`/usr/local/cuda`), la configuration échouait sur « CUDA Toolkit not found », après avoir pourtant affiché la version de CUDA. AJEAN cherche maintenant le toolkit avant le raccourci du PATH, et indique explicitement sa racine à CMake. Si le toolkit manque vraiment, le message le dit au lieu de laisser l'erreur brute de CMake.

## L'installation d'un moteur ne disparaît plus des écrans

L'avancement d'une installation vivait uniquement en mémoire du service. Au moindre redémarrage (mise à jour du binaire, plantage, reboot), la page rechargée n'affichait plus rien : impossible de savoir si la compilation continuait. Elle ne continuait pas, car elle meurt avec le service, mais personne ne le disait.

L'état est maintenant écrit sur disque et rechargé au démarrage. Une opération interrompue est annoncée comme telle, avec son journal. Et sur téléphone, où le panneau latéral est fermé, une pastille en haut de l'écran rappelle qu'une installation est en cours et y ramène en un clic.

## Choisir ses cartes graphiques par modèle

Nouvelle section « Cartes graphiques » dans l'éditeur de modèle, visible seulement si la machine en a plusieurs. Un interrupteur par carte, et une barre de répartition sur laquelle on fait glisser la part de chacune.

La liste des cartes est demandée au moteur du preset, pas au système : les identifiants et leur ordre appartiennent au backend. Sur une même machine, un moteur CUDA annonce `CUDA0` pour la grosse carte quand un moteur Vulkan annonce `Vulkan0` pour la petite. Une liste générique aurait fait sélectionner la mauvaise.

Le réglage passe par `--device`, compris par tous les backends, là où la variable `CUDA_VISIBLE_DEVICES` utilisée par la commande `ajean gpu` reste sans effet sur un moteur Vulkan.

## Réglages regroupés et décodage spéculatif

Les « réglages avancés » ne sont plus repliés dans un menu : BATCH, UBATCH, threads de traitement par lots, experts MoE sur CPU et chargement complet en mémoire sont à la suite des autres, avec tous les interrupteurs regroupés en fin de liste.

Le décodage spéculatif se règle maintenant visuellement : le type (MTP, EAGLE 3, n grammes et les autres valeurs acceptées par le moteur) et le nombre de jetons anticipés, ce dernier n'apparaissant qu'une fois un type choisi.

## Corrections d'interface

La sélection de cartes faite avec `ajean gpu` était effacée au changement de preset, tout comme le choix du moteur web. Ces réglages survivent maintenant à une bascule, sauf quand le preset les définit lui même, auquel cas c'est le preset qui gagne (un preset multi GPU impose sa configuration).

Les listes repliées affichaient une double bordure en bas. La croix de fermeture des fenêtres était écrasée en largeur, elle est maintenant carrée et plus facile à viser au doigt.

## Mise à jour

```
ajean update
```

Testé sur Linux avec un moteur CUDA compilé et le moteur Vulkan précompilé, sur deux cartes NVIDIA. La section « Cartes graphiques » n'a pas été essayée sur une machine ROCm ou Metal, ni sur Windows. Le support macOS reste non testé sur du matériel Apple.
