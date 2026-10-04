Recherche dans le texte des conversations, vidéos comprises par le modèle, et réglages du moteur plus transparents.

## Historique

- **Recherche dans le texte des conversations.** La recherche de l'historique ne regardait que les titres : une conversation dont le titre ne rappelait rien restait introuvable. Elle trouve désormais aussi les mots écrits dans les échanges (début de mot, sans tenir compte des accents), titres en tête des résultats. L'index est chiffré comme les conversations quand la mémoire l'est, et les anciennes conversations sont indexées en tâche de fond après la mise à jour. Contribution d'Olioli4 (#99).

## Vision

- **Vidéos.** Avec un modèle qui lit la vidéo (Qwen3.8 par exemple) et son projecteur vision, une vidéo jointe au message (mp4, webm, mov, mkv, avi) est transmise au modèle, et en mode agent l'outil `see_video` lui permet d'en ouvrir une sur le disque. Nécessite un llama.cpp récent et ffmpeg sur la machine du moteur. Contribution de Trombo38 (#100).

## Configuration

- **Commande du moteur.** Le bloc Configuration affiche la ligne de commande llama-server exacte du dernier lancement, copiable en un clic (clé API masquée) : pratique pour voir ce qu'AJEAN ajoute, ou partager un réglage (#108).
- **Nombre de couches du modèle.** Le nombre de couches est lu dans le fichier du modèle et affiché à côté de NGL, dans le bloc Configuration et dans l'éditeur de preset. Il devient facile de baisser NGL juste ce qu'il faut pour laisser de la VRAM au contexte (#43).

## Corrections

- **Aide des réglages grisés.** Le « ? » d'aide ne réagissait plus au survol quand le réglage était grisé (mode agent désactivé). Il reste maintenant consultable (#104).

## Mise à jour

    ajean update

Vérifié par les tests automatiques. La lecture vidéo et le nouvel affichage de la configuration n'ont pas été essayés sous Windows ni sur macOS.
