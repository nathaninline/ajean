La limite d'envoi de fichiers passe de 24 Mo à 1 Go.

## Des fichiers d'un gigaoctet

La 0.8.7 plafonnait à 24 Mo, et ce n'était pas un caprice : le fichier partait dans un seul message, encodé en base64, gardé entier en mémoire par le navigateur puis par le serveur. Un gigaoctet ainsi transmis, c'est près d'un gigaoctet et demi de mémoire de chaque côté — de quoi faire tomber la machine.

Le fichier part maintenant par tranches de 8 Mo. Le navigateur n'en lit qu'une à la fois, le serveur l'écrit sur le disque et l'oublie aussitôt : plus personne ne détient le fichier entier. Mesuré sur un envoi de 120 Mo, la mémoire du serveur n'a pas bougé d'un mégaoctet.

Au passage, la vignette affiche l'avancement en pourcentage : sur un gros fichier, l'anneau qui tournait sans rien dire n'était pas d'un grand secours.

Un envoi interrompu — onglet fermé, réseau coupé, service redémarré — ne laisse rien derrière lui : le fichier partiel est effacé au bout de dix minutes, et au démarrage du service.

Ce qui n'a pas changé : le dépôt n'a toujours lieu qu'à l'envoi du message, et l'accès distant fonctionne pareil, les tranches passant par le tunnel chiffré comme le reste.

Pensez tout de même à la place disque de la machine qui héberge AJEAN : les fichiers envoyés s'accumulent dans `uploads/`, à l'intérieur du dossier de travail de l'IA.

## Mise à jour

```bash
ajean update
```

Puis redémarrez l'interface :

```bash
ajean ui restart
```
