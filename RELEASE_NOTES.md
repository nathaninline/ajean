Introduction des projets : chaque projet possède sa propre mémoire et ses propres conversations, cloisonnées les unes des autres. La mémoire n'est plus un espace unique partagé, et un index de mémoire par projet est tenu à jour automatiquement.

## Projets

* Nouveau système de projets, sur le modèle des projets d'un assistant de code. Un projet regroupe une mémoire indépendante (des pages Markdown et un index) et son propre jeu de conversations. Plus de mémoire globale partagée : on crée un projet par sujet, chacun avec sa mémoire et ses fils, isolés les uns des autres.

* Un bouton dédié dans la zone de saisie ouvre un panneau unique qui gère à la fois les projets (créer, renommer, supprimer, basculer) et les conversations du projet actif (nouvelle, ouvrir, renommer, mettre en favori, supprimer, exporter).

* Migration automatique au premier démarrage : toute la mémoire et toutes les conversations existantes sont regroupées dans un projet « Générale », qui devient le projet actif par défaut. Rien n'est perdu.

* Les tâches planifiées peuvent viser un projet précis : la tâche lit et écrit alors dans la mémoire de ce projet.

## Mémoire et index

* Chaque projet possède un index (MEMORY.md) tenu à jour automatiquement par le programme : créer une page ajoute sa ligne, en supprimer une la retire. L'index n'est pas une note ordinaire et n'apparaît plus dans la liste des pages.

* L'index est fourni à l'assistant une fois au début de la conversation, puis de nouveau après une compaction, au lieu d'être reconstruit à chaque tour. L'assistant dispose ainsi en permanence de la liste des pages disponibles.

* La recherche dans la mémoire n'est plus un passage obligé : l'assistant s'appuie sur l'index pour ouvrir directement la bonne page, et ne recherche par contenu que lorsque l'index ne suffit pas.

* Le prompt système personnalisé se règle désormais par preset, dans l'éditeur de preset, et s'applique lorsque le preset correspondant est actif.

## Interface

* Refonte des fenêtres Projets et Postes distants dans un style épuré : listes aérées, moins de cadres, sélection mise en valeur par une teinte plutôt que par un contour.

* Boutons d'envoi et d'arrêt redessinés en pastilles rondes (flèche et carré), neutres selon le thème.

* Nouveau bouton d'action à côté de l'envoi : il déploie un menu (joindre un fichier, postes distants, et compacter le contexte au-delà de la moitié). Un indicateur signale lorsque l'assistant agit sur un poste distant.

* Voile de chargement remplacé par la marque du logo, avec une durée minimale d'affichage pour éviter tout clignotement.

* Plusieurs sections du menu renommées, et divers ajustements d'espacement et de cohérence.

## Corrections

* Le changement de machine cible ne se bloque plus lorsque l'assistant revient sur une machine déjà utilisée.

* Une conversation archivée peut être exportée directement depuis son menu, sans avoir à l'ouvrir.
