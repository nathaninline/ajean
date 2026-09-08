Connexion d'une API externe compatible OpenAI (OpenAI, Claude, Groq, OpenRouter, ou tout serveur compatible Chat Completions) comme source de génération à la place du moteur local, accès à la mémoire du projet depuis une fenêtre dédiée ouverte par le menu d'ajout, et plusieurs améliorations d'interface.

## Nouveautés

* **Connexion d'une API externe.** Un nouveau bouton (icône globe), à gauche du « + » de la section Presets, ouvre une fenêtre pour configurer une IA distante compatible OpenAI Chat Completions : URL de l'API, modèle, clé, et taille de contexte, avec un bouton pour tester la connexion. Une fois le preset externe sélectionné, la génération est routée vers l'API distante au lieu du moteur local ; le moteur local est alors arrêté pour libérer la mémoire. Les presets externes apparaissent dans la liste avec leur propre icône et le nom du modèle distant. Le mode agent (outils, mémoire, accès web) reste disponible avec un modèle distant compatible.
* **Mémoire du projet dans une fenêtre dédiée.** La mémoire du projet (mode d'utilisation et pages) quitte la barre latérale pour une fenêtre à part, ouverte depuis le menu d'ajout de la zone de saisie. Le mode se règle par un sélecteur à trois options (auto, sur demande, désactivée) et chaque page se gère depuis un menu par carte.

## Interface

* La croix de fermeture des fenêtres, jusqu'ici légèrement décentrée dans son carré selon la police, est désormais dessinée et centrée avec précision.
* Espacements, icônes et libellés revus dans les fenêtres concernées pour une lecture plus aérée, en particulier sur mobile.

## Note

La connexion externe a été validée contre un serveur compatible OpenAI ; le comportement exact peut varier selon le fournisseur distant (champs acceptés, gestion des outils).

## Mise à jour

```
ajean update
```
