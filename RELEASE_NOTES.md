Une seule correction, mais elle touche l'accès distant.

## Changer la clé de pilotage ne coupe plus ajean.link

Si vous changiez la clé de pilotage, depuis l'interface ou avec `ajean set-web-key`, l'accès distant tombait aussitôt et ne revenait qu'au redémarrage du service.

La cause : le tunnel lisait la clé une seule fois, à son ouverture, puis l'injectait dans chaque requête venue du relais. Après un changement, il continuait donc à présenter l'ancienne, et tout passait en refus d'authentification.

Le symptôme, lui, ne disait rien du problème. Le chat n'affiche pas le code d'erreur d'un flux qui n'arrive jamais : on voyait « chargement de la conversation » tourner à l'infini, sans un mot d'explication, alors que le serveur allait très bien.

La clé est maintenant relue à chaque requête. Si l'accès distant vous a lâché après un changement de clé, cette version suffit à le rétablir.

## Mise à jour

```bash
ajean update
```

Puis redémarrez le service d'interface pour rouvrir le tunnel :

```bash
ajean ui restart
```

Si vous utilisez le portail, rechargez la page ensuite.
