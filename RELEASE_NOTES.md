## Correction

**Lancer le fichier téléchargé pendant qu'AJEAN tourne.** Windows désigne un même fichier sous plusieurs écritures, par exemple `Administrateur` et sa forme abrégée `ADMINI~1`. AJEAN comparait ces chemins tels quels, sans les ramener à une écriture commune. Il pouvait donc ne pas reconnaître l'application en cours d'exécution, remplacer le programme en la croyant fermée, et vous laisser avec l'ancienne version toujours à l'écran. C'est le comportement corrigé en 0.6.12, qui revenait par un autre chemin.

Le même défaut pouvait, dans le cas où le dossier de données est désigné par une écriture abrégée, amener l'application installée à se prendre pour une copie téléchargée et à se relancer sans fin.

Les chemins sont désormais ramenés à une écriture unique avant d'être comparés.

## Vérification

Les six situations du lancement sous Windows ont cette fois été jouées sur une machine réelle, avec trois versions distinctes du programme : mise à jour silencieuse application fermée, application en cours avec acceptation puis avec refus, fichier plus ancien que la version installée avec les deux réponses, et recréation des raccourcis supprimés. Le contenu des fenêtres et les numéros de version affichés ont été relus, et chaque essai vérifie que la fenêtre s'est bien refermée avant de conclure.

C'est ce banc d'essai qui a mis au jour le défaut ci-dessus, que trois relectures du code avaient laissé passer.

## Mise à jour

```
jean update
```

Sous Windows, télécharger le fichier depuis la page des releases et le lancer fait le même travail.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
