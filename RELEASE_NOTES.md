Cette version refait entièrement `ajean chat`, le chat en terminal : plus agréable à utiliser, plus lisible, et pensé pour travailler directement dans un dossier.

## Un chat terminal simple et rapide

`ajean chat` reste volontairement léger : une conversation locale au terminal (indépendante de l'interface web), sans mémoire ni projets. Le modèle dispose de trois outils, `bash`, `write` et `edit`, qui agissent **dans le dossier où la commande est lancée**. Le prompt système est court et adapté au terminal. `/tools off` coupe les outils pour des réponses texte uniquement.

## Affichage

- En-tête de session : modèle, taille du contexte, dossier de travail et outils disponibles.
- Indicateur d'activité avec chronomètre pendant la réflexion et l'exécution des outils.
- Le raisonnement du modèle est replié en une ligne (« Réfléchi pendant 2,1 s ») ; `/think` l'affiche en direct.
- Chaque appel d'outil tient en deux lignes : la commande, puis le résultat (code de sortie, nombre de lignes, durée, premières lignes de sortie). Les modifications de fichiers montrent les lignes ajoutées et retirées.
- La réponse est mise en forme pendant qu'elle s'écrit : titres, gras, listes, blocs de code encadrés, citations, liens.
- Sous chaque réponse : durée, tokens générés, vitesse et remplissage du contexte (signalé au-delà de 80 %).

## Saisie

- Édition complète : flèches, mot par mot, début et fin de ligne, raccourcis Ctrl-A, Ctrl-E, Ctrl-U, Ctrl-K, Ctrl-W.
- Historique conservé d'une session à l'autre (flèches haut et bas).
- Messages sur plusieurs lignes : Alt+Entrée, Ctrl+J, ou « \ » en fin de ligne. Un texte collé sur plusieurs lignes n'est pas envoyé avant Entrée.
- Complétion des commandes avec Tab et suggestion grisée.
- Les caractères accentués fonctionnent sous Windows.

## Contrôle

- Échap ou Ctrl-C interrompt une réponse sans quitter la session. Le texte tapé pendant la génération est conservé pour le message suivant.
- Ctrl-D, ou Ctrl-C deux fois, quitte.
- Si le moteur est arrêté, `ajean chat` propose de le démarrer puis attend le chargement du modèle.

## Commandes

`/new`, `/retry`, `/undo`, `/copy` (presse-papiers), `/save` (conversation en Markdown), `/system`, `/tools`, `/think`, `/model` (liste les presets et bascule, preset externe compris), `/help`, `/quit`.

## Scripts

`ajean chat -p "question"` donne une seule réponse puis rend la main, de même que `echo texte | ajean chat` ou une sortie redirigée vers un fichier.

**Changement de comportement** : le texte passé en argument (`ajean chat "texte"`) est désormais le premier message envoyé, et non plus le prompt système. Le prompt système se définit avec `--system "texte"`.

## Divers

La variable d'environnement standard `NO_COLOR` désactive les couleurs dans toutes les commandes.

## Mise à jour

    ajean update

Non testé : la version macOS (compilée par la CI, pas vérifiée sur un Mac) et l'ancienne console Windows hors Windows Terminal. Vérifié sous Windows (terminal ConPTY) et sur un serveur Linux.
