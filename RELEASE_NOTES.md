## Correction

**L'installation échouait sur un ordinateur vierge.** Au tout premier lancement du fichier téléchargé, sur une machine où AJEAN n'avait jamais été installé, la fenêtre d'installation s'affichait, puis répondait :

```
Installation impossible :
open C:\ProgramData\jean\bin\jean.exe: The system cannot find the path specified
```

AJEAN démarrait alors depuis le fichier téléchargé, sans s'être installé : aucun raccourci, aucun programme dans le menu Démarrer, et le fichier téléchargé restait le seul moyen de le lancer.

La cause : le premier lancement créait bien le dossier de réglages, mais pas le sous-dossier destiné à recevoir le programme. Il n'était créé que par l'installation en ligne de commande. Le dossier est désormais créé au moment d'écrire le programme, quel que soit le chemin emprunté.

Ce cas échappait aux essais des versions précédentes, qui partaient toujours d'une installation déjà en place. Le parcours complet sur une machine vierge est maintenant vérifié, fenêtre après fenêtre, jusqu'au démarrage de l'application et à la présence des raccourcis.

## Mise à jour

Si vous êtes dans ce cas, rien n'est à réparer : téléchargez `jean-windows-amd64.exe` depuis cette page et lancez-le, l'installation se fera cette fois.

Pour une installation déjà en place :

```
jean update
```

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
