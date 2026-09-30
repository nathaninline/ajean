Correctif de la 0.17.0 : fenêtre AJEAN grise au lancement sous Windows, et une conversation garde désormais son mode.

## Corrections

- **Windows : fenêtre grise et inerte au premier lancement** (#101). Depuis l'ouverture dans une fenêtre dédiée (0.16.6), la première fenêtre restait un cadre gris impossible à déplacer, qu'il fallait fermer en tuant Edge, ce qui fermait aussi AJEAN. Le navigateur était lancé avec une consigne « fenêtre masquée » destinée aux programmes en console, qu'Edge appliquait à la fenêtre AJEAN. La fenêtre s'affiche maintenant normalement dès le premier lancement.

## Modes de chat

- **Une conversation garde le mode dans lequel elle a commencé** (Rapide, Projet ou Modèle de base). Changer de mode pendant une conversation en démarre une nouvelle, et rouvrir une conversation depuis l'historique (ou depuis un autre appareil) remet le sélecteur sur son mode. Le prompt, les outils et le contexte restent ainsi cohérents d'un bout à l'autre de la conversation.
- Si le premier message est refusé (modèle encore en chargement), le mode n'est pas figé.

## Interface

- Plus de mention « (pas de GPU) » dans la carte Appareil ni « (aucune tâche) » dans la section Tâches : ces zones restent simplement vides.

## Mise à jour

    ajean update

Vérifié sous Windows (fenêtre AJEAN avec un profil neuf, relance) et dans le navigateur. Non testé sur macOS.
