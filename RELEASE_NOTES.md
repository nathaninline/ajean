Refonte complète de l'interface : un seul langage visuel sobre et sans bordures, trois modes de chat, un historique général de toutes les conversations et une nouvelle police.

## Modes de chat

- **Sélecteur de mode** à gauche de la zone de saisie, réglé par appareil :
  - **Rapide** (par défaut) : fonctionne comme `ajean chat` dans le terminal. Prompt court, seulement les outils `bash`, `write` et `edit`, ni mémoire, ni projet, ni web.
  - **Projet** : le chat complet, avec mémoire, projets, trackers et accès web.
  - **Modèle de base** : le modèle brut, sans agent, sans outils, sans mémoire et sans prompt système.
- La section « Agent » du menu disparaît : choisir Projet ou Rapide active l'agent de la machine (avec la même confirmation qu'avant). Les réglages restants (accès internet, contrôle du navigateur, serveurs MCP, compactage, notifications, chiffrement) sont regroupés dans une section **Paramètres**, en dernière position.
- Correction : en « Modèle de base », le modèle recevait encore le préambule « Jean + mémoire persistante » et l'index de la mémoire. Ce mode n'injecte désormais plus rien, et le contexte de projet d'une conversation commencée en mode Projet est retiré de ce que voit le modèle en Rapide ou en Modèle de base.

## Historique et projets

- **Historique général** : l'icône horloge en haut du menu remplace le menu par l'historique de toutes les conversations, tous projets confondus. Le nom du projet est indiqué sur chaque ligne, et ouvrir une conversation d'un autre projet bascule sur ce projet.
- **Recherche** dans l'historique (titre et nom du projet, sans tenir compte des accents ni de la casse).
- Chaque conversation a un menu ⋮ : favori, renommer, déplacer vers un projet, exporter, supprimer.
- L'historique se charge par pages et est préchargé : l'ouverture est instantanée même avec des centaines de conversations.
- **Projets** : la fenêtre des projets est remplacée par une liste rapide dans la bulle projet de la zone de saisie, avec un menu ⋮ par projet (renommer, options, voir la mémoire, supprimer) et « Nouveau projet ».
- Mémoire et trackers sont accessibles depuis l'en-tête de l'historique.
- Nouvelle conversation : dans le menu « + » de la zone de saisie.

## Interface

- **Nouvelle police** : Inter, embarquée dans l'application (fonctionne hors ligne et via app.ajean.link).
- **Plus de bordures** : cartes, champs, boutons, badges, menus et fenêtres passent sur des fonds teintés arrondis. Toutes les fenêtres partagent le même design, et le thème sombre adopte une palette un peu plus claire et plus régulière.
- **Menus unifiés** : listes déroulantes, menus de mode, de projet, « + » et ⋮ utilisent tous le même menu, animé, au lieu des listes natives du navigateur.
- **Sélecteurs segmentés** (exécution, moteur, transport MCP, type de tâche) : une pastille glisse sous le choix actif.
- **Animations** : ouverture et fermeture des sections du menu en fondu, bascule menu / historique, envoi d'un message, passage envoyer ⇄ stop en fondu enchaîné, apparition du texte de l'IA en fondu pendant qu'il s'écrit.
- Barres de défilement fines et discrètes partout ; dans le menu latéral, la barre flotte au bord et n'apparaît qu'au défilement.
- Presets en tuiles, le preset actif sur un fond plus marqué ; version affichée à côté du logo, état du moteur en dessous.
- Sur mobile, l'application s'ouvre sur un bouton burger arrondi.

## Mise à jour

    ajean update

Vérifié sur un serveur Linux et dans le navigateur (thèmes clair et sombre, ordinateur). Les animations et l'affichage sur mobile ont été moins testés : les retours sont bienvenus.
