Cette version supprime le long temps d'attente au premier message après un changement de projet.

## Changement de projet : le prompt n'est plus recalculé en entier

Après être passé d'un projet à un autre, le premier message prenait autant de temps qu'une conversation neuve (environ 8 secondes pour 5 000 tokens sur un Qwen3.8 27B), alors qu'une nouvelle conversation dans le même projet démarrait en une ou deux secondes.

Avec les modèles hybrides (Qwen3.5 et suivants), llama.cpp ne peut reprendre un prompt déjà calculé qu'au début d'un message utilisateur. Or tout ce qui dépend du projet (son nom, sa description, l'index de sa mémoire et la liste de ses trackers) se trouvait dans le prompt système : les deux projets divergeaient avant le premier point de reprise, et tout était recalculé.

Ces éléments sont désormais placés en tête du premier message utilisateur envoyé au modèle. Le prompt système est identique d'un projet à l'autre et reste en cache : au changement de projet, seule la partie propre au projet est recalculée. L'historique enregistré et l'affichage des conversations ne changent pas.

Le gain ne vaut qu'entre projets qui utilisent le même mode mémoire : la consigne de mémoire fait partie du prompt système et change avec le mode.

## Mise à jour

    ajean update

Non testé : la version macOS (compilée par la CI, pas vérifiée sur un Mac). Vérifié sur un serveur Linux avec Qwen3.8 27B uniquement.
