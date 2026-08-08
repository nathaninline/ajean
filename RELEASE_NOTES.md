Une correction qui compte, et la fenêtre d'export refaite d'après vos retours.

## Le chat qui s'arrête sans un mot

Symptôme rapporté : l'agent réfléchit, lance quelques commandes, puis s'arrête au milieu du travail et rend la main sans avoir terminé ni commenté. Aucune erreur nulle part, et dans le journal du moteur un « stop processing » parfaitement normal.

La cause est dans AJEAN, pas dans le moteur. La lecture du flux de réponse s'arrête aussi bien à la fin normale qu'à la première erreur, et AJEAN ne regardait jamais laquelle des deux venait de se produire. Une connexion coupée en plein milieu était donc traitée comme une réponse terminée : le tour se refermait en silence.

Plus ennuyeux, un cas que personne n'avait encore vu passer : quand la coupure tombait pendant que le modèle demandait un outil, l'appel à moitié reçu était exécuté quand même, avec des arguments tronqués.

Désormais, un flux coupé est annoncé, avec la cause exacte dans le message, et le tour est abandonné plutôt que de lancer un appel incomplet. Le texte déjà reçu reste affiché. Un arrêt que vous demandez vous même reste silencieux, comme avant.

À noter : cette correction rend la coupure VISIBLE, elle ne l'empêche pas. Si le message apparaît chez vous, l'erreur qu'il nomme (« unexpected EOF », « connection reset », « ligne trop longue »…) est exactement ce qu'il nous faut pour trouver la suite. Signalez la.

La taille maximale d'une ligne du flux passe de 1 à 8 Mio au passage : la seule chose qui puisse la dépasser est un appel d'outil démesuré, et jusqu'ici il partait dans le même silence.

## La fenêtre d'export

Elle est arrivée en 0.8.4 sous forme de deux boutons qui ne laissaient aucun choix. C'est maintenant une vraie fenêtre.

Le format, Markdown à lire ou JSON à retraiter, ne décide plus que du contenant : les trois mêmes options de contenu valent pour les deux, raisonnements, appels d'outils, sorties des outils. La version précédente proposait des options différentes selon le format, dont un obscur « journal d'affichage » côté JSON que personne ne pouvait comprendre.

La portée se règle à présent avec un curseur qui va de 1 au nombre réel d'échanges de votre conversation, butée à droite pour tout prendre, au lieu de valeurs prédéfinies qui proposaient « 25 derniers échanges » sur un fil qui en comptait trois.

Conversation vide, la fenêtre le dit simplement au lieu d'offrir des réglages pour un fichier qui serait vide.

Enfin, « clear chat » descend en dernière position du panneau Actions et se retrouve seul sur sa ligne : c'est la seule action destructive de ce panneau, elle n'a rien à faire sous le doigt qui visait « refresh ».

## Corrections plus discrètes

Le prompt système et la description de l'outil de recherche en mémoire annonçaient encore au modèle une mémoire rangée dans `MEMORY/`, nom d'avant la 0.8. Il pouvait donc aller chercher un dossier qui n'existe plus. Merci à qui l'a remarqué.

Le mécanisme qui met de côté l'ancien binaire pendant une mise à jour tirait son nom de l'horloge seule. Sous Windows, deux appels rapprochés pouvaient obtenir la même valeur, et le second écrasait le binaire mis de côté par le premier.

## Mise à jour

```bash
ajean update
```

Puis, si vous utilisez l'accès distant, rechargez la page du portail pour prendre la nouvelle interface.

Ce qui n'a pas été vérifié sur machine réelle : le téléchargement d'un modèle découpé en plusieurs fichiers de bout en bout (arrivé en 0.8.4, couvert par des tests mais pas par un vrai transfert), et le comportement sur macOS, toujours pas testé sur un Mac.
