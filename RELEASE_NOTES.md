La limite d'envoi de fichiers passe de 24 Mo à 1 Go, et le téléchargement depuis ajean.link est réparé.

## Des fichiers d'un gigaoctet

La 0.8.7 plafonnait à 24 Mo, et ce n'était pas un caprice : le fichier partait dans un seul message, encodé en base64, gardé entier en mémoire par le navigateur puis par le serveur. Un gigaoctet ainsi transmis, c'est près d'un gigaoctet et demi de mémoire de chaque côté — de quoi faire tomber la machine.

Le fichier part maintenant par tranches de 8 Mo. Le navigateur n'en lit qu'une à la fois, le serveur l'écrit sur le disque et l'oublie aussitôt : plus personne ne détient le fichier entier. Mesuré sur un envoi de 120 Mo, la mémoire du serveur n'a pas bougé d'un mégaoctet.

Au passage, la vignette affiche l'avancement en pourcentage : sur un gros fichier, l'anneau qui tournait sans rien dire n'était pas d'un grand secours.

Avant d'entamer un envoi, AJEAN vérifie qu'il reste de la place sur le disque et refuse franchement s'il n'y en a pas — remplir le volume de la machine qui fait tourner le modèle serait autrement plus ennuyeux qu'un envoi rejeté.

Un envoi interrompu — onglet fermé, réseau coupé, service redémarré — ne laisse rien derrière lui : le fichier partiel est effacé au bout de dix minutes, et au démarrage du service.

## Télécharger un fichier depuis ajean.link

Depuis l'accès distant, cliquer sur un fichier proposé par l'IA téléchargeait un contenu JSON illisible à la place du fichier.

La cause tient à la façon dont l'accès distant chiffre les échanges : toute réponse est réemballée en JSON avant de vous parvenir, et des données binaires n'y survivent pas. Le fichier était donc détruit en chemin, quel que soit son type.

Votre serveur signale maintenant à l'interface qu'elle est derrière le tunnel, et lui envoie le fichier sous une forme qui traverse intact, toujours en tranches pour ne pas le charger entier en mémoire. En local, rien ne change : le fichier est servi directement, sans détour.

Le même défaut abîmait l'export de conversation au format Markdown, qui revenait truffé de guillemets et d'échappements. Corrigé aussi.

## Aussi

- Plusieurs fichiers joints en même temps s'envoient désormais en parallèle. Ils s'attendaient les uns les autres, chaque envoi bloquant les suivants jusqu'à sa dernière tranche.

## Mise à jour

```bash
ajean update
```

Puis redémarrez l'interface :

```bash
ajean ui restart
```

Pensez à la place disque de la machine qui héberge AJEAN : les fichiers envoyés s'accumulent dans `uploads/`, à l'intérieur du dossier de travail de l'IA, et rien ne les efface pour l'instant.
