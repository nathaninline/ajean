Moteur MoE : partage des couches entre deux cartes, et démarrage d'un preset MoE en ligne de commande corrigé.

## Nouveautés

- **Seconde carte en partage des couches.** Dans les réglages d'un modèle du moteur MoE (section Moteur), la ligne « Seconde carte » propose maintenant trois choix : « Non utilisée », « Aide aux experts » (le mode existant, qui reste le défaut) et « Partage des couches ». Dans ce dernier mode, les deux cartes se répartissent les couches du modèle selon leur VRAM libre. Avec deux cartes identiques, la seconde carte était peu sollicitée en mode d'aide (environ 15 %), et un utilisateur a mesuré +50 % en génération avec le partage des couches. Les presets existants ne changent pas. (#121)

## Corrections

- **`ajean start` et `ajean restart` refusaient un preset MoE.** Le contrôle avant démarrage réclamait un moteur llama.cpp et un fichier de modèle, qu'un preset MoE n'a pas. Il vérifie désormais ce dont le moteur MoE a besoin (sa configuration et son environnement Python).
- **Sortie d'une commande perdue par l'outil shell de l'IA.** Quand une commande laissait un process tourner en arrière-plan (par exemple `./serveur &`), l'outil renvoyait parfois « WaitDelay expired » au lieu de ce que la commande avait affiché. La sortie est maintenant renvoyée, avec la mention qu'un process tourne encore.

## Mise à jour

    ajean update

Vérifié par les tests automatiques. Non testé sur du matériel réel : le partage des couches entre deux cartes.
