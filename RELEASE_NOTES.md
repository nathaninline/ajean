Ton serveur peut enfin te prévenir quand la réponse est prête, même l'app fermée ou le téléphone verrouillé. Et plusieurs petits accrocs de l'app mobile sont réglés.

## Notifications

* Nouveau : le serveur t'envoie une notification à la fin de chaque réponse, même quand l'app est fermée ou le téléphone verrouillé. Pratique pour lancer une longue génération et lâcher le téléphone. Ça passe par le Web Push (le serveur pousse directement vers Apple ou Google), donc pas besoin de garder l'app ouverte.
* À activer par appareil, dans Mode agent, juste sous « compactage automatique » et « gestion des machines ». Sur iPhone il faut d'abord ajouter AJEAN à l'écran d'accueil, puis activer depuis l'app installée (c'est une contrainte d'iOS, qui n'autorise les notifications que pour les apps installées).

## Application mobile (app.ajean.link)

* Au lancement de l'app installée, tu arrives directement sur ton dernier serveur au lieu de repasser à chaque fois par la liste « Mes serveurs ». Un glissement depuis le bord gauche ramène à la liste si tu veux en changer.
* Plus de flash blanc au lancement quand tu es en thème sombre : le bon thème est appliqué avant le premier affichage.
* Le petit écran de chargement ne clignote plus un « ajean.link » disgracieux au démarrage.

## Interface

* Les boutons menu et stop répondent de nouveau de façon fiable pendant que la réponse défile. Avant, sur téléphone, un appui sur deux pouvait être ignoré parce que le fil se repositionnait en permanence pendant l'écriture.
* L'animation des trois points du chargement d'une conversation refonctionne (elle était figée depuis quelques versions : son animation avait été retirée par erreur).

> Note : version interface et mobile, le moteur d'inférence n'est pas touché (aucun rechargement de modèle à la mise à jour). Vérifiée sur le serveur de Alice (Linux) et l'app iPhone ; le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
