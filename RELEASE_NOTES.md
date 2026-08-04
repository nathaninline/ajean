## Lancement sous Windows

**Le fichier téléchargé met AJEAN à jour et démarre.** Si AJEAN est déjà installé sur la machine, lancer `jean-windows-amd64.exe` remplace le programme installé par cette version, puis ouvre l'application. Sans question, sans message : c'est la seule chose qu'on puisse attendre d'un fichier qu'on vient de télécharger.

La 0.6.10 se contentait de signaler qu'AJEAN était déjà installé ailleurs, puis démarrait quand même la copie téléchargée. Une information dont on ne pouvait rien faire, suivie d'un comportement qu'elle n'annonçait pas.

À la toute première installation, en revanche, AJEAN demande toujours : c'est le seul moment où savoir ce qui est posé sur la machine, et à quel endroit, change quelque chose. Un refus est désormais mémorisé au lieu d'être reposé à chaque lancement.

## Interface

**Les emplacements ont rejoint le journal du moteur.** Le dossier de vos données, la configuration, la mémoire et le dossier de travail de l'IA s'affichent sous le journal, en cliquant sur la pastille d'état. Ils avaient leur propre bouton dans les actions en 0.6.10, loin de l'endroit où l'on va quand on cherche à comprendre son installation.

## Mise à jour

```
jean update
```

Sous Windows, télécharger le fichier depuis la page des releases et le lancer fait désormais le même travail.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas pu être vérifiée sur une vraie machine.
