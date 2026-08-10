Les fichiers circulent enfin dans les deux sens : vous pouvez en envoyer à l'IA, et récupérer ceux qu'elle produit.

## Envoyer un fichier

Un trombone est apparu dans le bas du composeur. Vous pouvez aussi glisser un fichier n'importe où dans la fenêtre, ou le coller avec Ctrl+V — une capture d'écran passe donc directement dans le chat.

Le fichier est déposé dans le dossier de travail de l'IA, sous `uploads/`, et son chemin est annoncé dans le message. Elle en fait ensuite ce qu'elle veut avec ses outils : le lire, le convertir, le lancer. Rien n'est interprété au passage, donc n'importe quel type de fichier fonctionne — à condition que le mode agent soit actif, sans quoi elle voit le nom mais n'a aucun outil pour l'ouvrir.

Deux détails qui comptent à l'usage. Le dépôt n'a lieu qu'à **l'envoi** du message : tant que vous n'avez pas cliqué, rien n'atterrit sur le disque, et retirer un fichier de la liste ne laisse aucune trace. Et joindre un fichier ne déplace pas la zone de saisie d'un pixel — les vignettes se posent au-dessus de la carte, dans l'espace qu'elle réservait déjà.

Le transfert passe par le tunnel chiffré comme le reste : l'envoi de fichiers fonctionne aussi depuis l'accès distant, sans rien configurer.

Taille maximale : 24 Mo par fichier.

## Recevoir un fichier

Quand vous demandiez un fichier à l'IA, elle répondait par un chemin sur le serveur — `/etc/ajean/workspace/rapport.pdf` — parfaitement inutilisable depuis un navigateur.

Elle écrit maintenant un lien Markdown ordinaire, et cliquer dessus télécharge le fichier :

> C'est prêt : [le rapport](rapport.pdf)

Aucune syntaxe particulière à connaître de son côté, et les liens web restent des liens web. Seul le dossier de travail est accessible ; un chemin qui en sort est refusé, et les fichiers sont toujours servis en téléchargement, jamais affichés.

## Un quart de contexte rendu au mode agent

Le préambule envoyé au modèle à chaque tour est passé d'environ 2350 à 1770 tokens, sans qu'une seule consigne disparaisse.

L'essentiel du poids n'était pas dans le texte du prompt mais dans les schémas des outils, qui pèsent le double. Le prompt réénumérait des outils que ces schémas décrivent déjà, et la règle « écris les fichiers avec `write`, jamais avec `echo` » y figurait trois fois. Elle ne vit plus que là où elle s'applique.

C'est autant de fenêtre rendue à la conversation, et un compactage qui arrive plus tard.

## Aussi

- L'export d'une conversation mentionne les fichiers joints. Un message envoyé sans texte, juste avec un fichier, y apparaissait comme une section vide.
- Le décompte de contexte, sous la zone de saisie, ne s'écrit plus « contexte 4210 / 32768 » mais « 4210 / 32768 » — la jauge juste en dessous dit déjà de quoi il s'agit, et la place gagnée sert au nom du modèle.

## Mise à jour

```bash
ajean update
```

Puis redémarrez l'interface :

```bash
ajean ui restart
```
