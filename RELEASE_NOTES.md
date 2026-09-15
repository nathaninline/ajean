Correction de l'installation du moteur sur les cartes NVIDIA anciennes.

## Compilation du moteur sur GPU NVIDIA anciens

Sur une machine équipée d'un GPU Pascal (GTX 10xx), Maxwell (GTX 900) ou Volta, l'installation du moteur pouvait échouer avec un message CMake incompréhensible du type « nvcc is not able to compile a simple test program ». La cause : CUDA 13 a supprimé la prise en charge de ces générations de cartes (Turing sm_75 est désormais le minimum), et l'installateur retenait systématiquement la version de CUDA la plus récente présente sur la machine, même quand elle ne savait plus compiler pour la carte.

Deux changements corrigent ce comportement :

- lorsque plusieurs versions du CUDA Toolkit sont installées, l'installateur retient maintenant la plus récente qui prend encore en charge la carte la plus ancienne de la machine, au lieu de la plus récente sans distinction. Une configuration multi-GPU mélangeant une carte ancienne et une carte récente est prise en compte : le toolkit choisi doit convenir aux deux ;
- lorsque aucune version de CUDA installée ne convient à la carte, l'installation s'arrête immédiatement avec un message clair indiquant la marche à suivre (installer un CUDA Toolkit 12.x, qui prend encore ces cartes en charge), au lieu de lancer une compilation vouée à l'échec.

Les machines dont la configuration fonctionnait déjà ne sont pas affectées : quand la version de CUDA la plus récente convient à la carte, c'est la même qui est retenue qu'auparavant, avec les mêmes réglages de compilation. Les performances du moteur sont inchangées.

## Mise à jour

    ajean update

Non testé sur une véritable machine à GPU Pascal ou Volta au moment de la publication : le correctif repose sur la table de compatibilité CUDA (versions 11, 12 et 13) et sur des tests unitaires de la logique de sélection.
