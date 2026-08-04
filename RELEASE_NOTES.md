## Le renommage du dossier se termine tout seul sous Windows

Jusqu'ici, sur un ordinateur où vous n'êtes pas administrateur, `C:\ProgramData\jean` ne pouvait pas devenir `C:\ProgramData\ajean` : renommer ce dossier demande un droit qu'un compte standard n'a pas. Le renommage restait donc à moitié fait, indéfiniment.

L'installation demande désormais l'autorisation Windows nécessaire, le temps du renommage, puis rend la main. La fenêtre apparaît au moment où vous installez ou mettez à jour AJEAN — jamais au démarrage ordinaire, et jamais si elle ne sert à rien : ni quand le dossier porte déjà le bon nom, ni quand vous avez choisi vous-même son emplacement.

Refuser cette autorisation est une réponse valable. AJEAN continue alors d'utiliser son dossier actuel, exactement comme avant. Rien n'est perdu, rien n'est bloqué, et la question reviendra à la prochaine installation.

## Mise à jour

```
ajean update
```

Sous Windows, télécharger `ajean-windows-amd64.exe` depuis cette page et le lancer fait le même travail.

Les binaires restent publiés sous leurs deux noms, `ajean-*` et `jean-*`, le temps que le parc bascule.
