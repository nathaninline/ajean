Les postes distants se pilotent maintenant depuis le chat, et l'envoi de fichiers à travers ajean.link est réparé.

## Les postes distants à portée de main

Un nouveau bouton apparaît dans la barre de saisie, juste à côté du trombone. Son icône dit **sur quelle machine l'IA agit** : un écran quand c'est ce serveur, des ondes wifi quand c'est un poste distant. Un clic ouvre une **fenêtre dédiée** où tu choisis la cible, vois tes postes appairés (avec leurs capacités et leur dossier autorisé) et en ajoutes de nouveaux.

Plus besoin d'aller fouiller dans les réglages : tout est là, au moment où on en a besoin.

## Envoi de fichiers réparé sur ajean.link

Depuis l'accès distant, joindre un fichier à un message échouait. La cause : l'envoi ne passait pas par le tunnel chiffré de bout en bout, contrairement au téléchargement. C'est corrigé — l'envoi emprunte désormais le même chemin, et un gigaoctet traverse toujours par morceaux, sans que le relais ne voie rien.

## Installer un poste, en une commande ré-exécutable

`ajean remote install …` peut maintenant être relancé tel quel : fournir un code d'appairage force toujours le (ré)appairage, et la commande retire proprement un service déjà en place au lieu d'échouer sur un fichier verrouillé. Fini le `logout` manuel entre deux essais.

## Mise à jour depuis l'interface, même sans root

Sur un serveur où le service tourne sous un compte non-root, le bouton « mettre à jour » de l'interface redémarre désormais correctement le service (via sudo non interactif) au lieu d'échouer en silence.
