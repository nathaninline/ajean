Version d'interface. Le menu de réglages suit désormais une seule et même grammaire de bout en bout, les modales s'ouvrent sans temps mort, et plusieurs irritants de l'app iPhone disparaissent.

## Un menu homogène

Chaque section obéit à la même règle : une étiquette de groupe, puis une carte, puis des lignes séparées par un filet. « Mode agent » était le plus voyant, ses quatre sous réglages (mémoire, accès internet, serveurs MCP, paramètres) formaient des îlots décollés, chacun avec son titre enfermé dans sa carte et des marges au cas par cas. Même traitement pour « Accès OpenAI » (trois cartes accolées pour un seul sujet, maintenant réunies), « Accès distant » (le bouton de démarrage du tunnel flottait entre deux cartes), « Moteur » (les trois choix llama.cpp étaient des cartes dans une carte, ce sont des lignes), « System prompt » et « Presets », dont le contenu flottait à nu sous le titre.

Les presets et les pages mémoire ne sont plus une pile de petites cartes bordées mais des lignes d'une seule carte. Le preset actif se signale par un voile et un liseré à gauche. Son bouton d'édition devient un engrenage.

Les boutons « copier » et « ouvrir » des adresses passent en icônes au trait dans un bouton carré à la hauteur du champ : les glyphes texte se posaient de travers et changeaient de largeur selon la police. Dans la mémoire, « Pages » devient « Voir la mémoire ». La pastille d'état n'affiche plus le port, qui reste visible là où il sert vraiment, dans l'adresse de l'endpoint OpenAI.

## Modales

Ouvrir un preset attendait la fin de trois requêtes avant d'afficher quoi que ce soit. En accès distant, le clic paraissait mort pendant une seconde. La modale s'ouvre maintenant tout de suite, avec un indicateur de chargement à la place du formulaire, et le formulaire apparaît complet d'un seul coup. La carte occupe sa taille finale dès l'ouverture, donc aucun à coup au moment où le contenu arrive.

Ouverture et fermeture sont animées, dans les deux sens, sur toutes les modales. L'animation se désactive si le système demande de réduire les animations.

Le pied de l'éditeur tient sur une seule ligne : la suppression devient une icône, et son option « supprimer aussi le fichier .gguf » se pose là où elle a du sens, dans la boîte de confirmation. Elle est décochée à chaque ouverture, aucun modèle ne peut donc être effacé parce que la case serait restée cochée d'une fois sur l'autre.

## iPhone

Dans l'app installée sur l'écran d'accueil, le tiroir du menu passait sous l'encoche, contrairement à la conversation. L'esquive d'encoche était bien posée, mais un bloc de style plus bas dans la feuille réécrivait la marge et l'effaçait.

Le rectangle gris que Safari peint sur tout élément touché est supprimé. Les états visuels propres à l'application, eux, sont conservés.

## Chat

Quand le raisonnement ou les appels d'outils sont masqués, l'indicateur d'activité disparaissait dès qu'une bulle arrivait, même invisible : le fil restait donc vide, sans aucun signe, pendant que le modèle travaillait. Il tient maintenant compte de ce qui est réellement affiché et reste présent jusqu'à l'arrivée de la réponse.

Une conversation vide affiche le logo AJEAN et une invitation, au lieu d'un écran nu.

## Mise à jour

```
ajean update
```

Les changements ont été vérifiés sur le rendu réel, en thème clair et sombre, et déployés sur un serveur de production avant publication. La correction PWA sous iOS découle de la cause identifiée dans la feuille de style, mais n'a pas été reproduite sur un appareil de test.
