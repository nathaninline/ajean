Correctifs d'interface dans la foulée de la 0.7.11. Tout ce qui bougeait, sautait ou clignotait après un chargement se tient enfin tranquille.

## L'éditeur de preset ne s'ouvre plus au milieu

Ouvrir un preset après en avoir fait défiler un autre rouvrait la fiche à la position précédente. La remise à zéro du défilement existait, mais elle était faite avant l'affichage : écrire une position de défilement sur un élément non affiché ne fait rien du tout, le navigateur restaurait donc l'ancienne. Elle se fait maintenant après l'affichage, puis une seconde fois une fois le contenu en place.

## Le sélecteur de moteur ne glisse plus tout seul

En passant d'un preset à l'autre, on voyait le curseur du sélecteur (précompilé, compilé, personnalisé) glisser de l'ancienne valeur vers la nouvelle une fois le chargement terminé. Le changement d'état avait pourtant lieu pendant que le formulaire était caché : WebKit diffère les transitions des éléments non rendus et les rejoue à leur retour à l'écran. Les transitions du formulaire sont donc coupées tant qu'il est masqué, et pendant l'image où il réapparaît.

Dans le même esprit, le voile de chargement de la modale est devenu opaque, sous le titre resté lisible. Rien du preset précédent ne peut plus apparaître une fraction de seconde.

## Le menu ne saute plus au chargement

Plusieurs blocs (config active, jauges machine, liste des presets, état de l'accès internet, serveurs MCP) sont vides tant que le serveur n'a pas répondu, puis prennent leur taille réelle : tout le menu se décalait d'un cran, ce qui se voyait surtout sur les sections du bas. Leur hauteur est désormais mémorisée d'une session à l'autre et réservée dès le démarrage, puis rendue quand le vrai contenu arrive. La réserve tient la place, elle n'affiche jamais d'information fausse.

Les champs Crawl4AI, eux, étaient visibles au départ puis repliés dès que l'état arrivait, le moteur par défaut étant l'intégré. Ils partent maintenant masqués. La section Moteur ne grandit plus non plus : son texte d'en-tête part à sa taille définitive et la ligne d'état des trois moteurs a sa hauteur réservée.

Au passage, un chargement en échec (accès distant coupé par exemple) n'interrompt plus les autres.

## Détails

Les pastilles « actif » et « inactif » disparaissent de l'accès internet et des serveurs MCP : l'interrupteur et la liste le disent déjà. Reste la seule information utile, l'anomalie « injoignable », qui signale que les outils web ne sont pas fournis au modèle.

Dans la mémoire, la ligne « Voir la mémoire » perd son chevron et son libellé est enfin centré. Créer une page annonçait « Nouveau Page » et demandait un « Nom du preset » : chaque type a maintenant ses propres libellés.

## Mise à jour

```
ajean update
```

Chaque correction a été vérifiée en mesurant le rendu réel, position et hauteur des éléments avant et après chargement, pas seulement à l'oeil.
