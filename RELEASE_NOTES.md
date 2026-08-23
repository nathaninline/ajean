L'historique des conversations devient un vrai espace « Sessions » qu'on gère (renommer, favori, garder ses fils sans les perdre), et la recherche dans la mémoire de l'IA remonte enfin les bonnes pages.

## Sessions (ex-Historique)

* Le modal « Historique » devient « Sessions » : chaque conversation est une session persistante que tu peux ouvrir, renommer et mettre en favori, et elle reste dans la liste. Fini le fil qu'on recharge et qui disparaît.
* Ouvrir une session sauvegarde d'abord celle en cours dans la sienne (même identifiant, nom et favori conservés) : rien ne se perd, aucun doublon. Le nom et le favori survivent maintenant à un aller-retour recharger puis nouvelle conversation, ce qui n'était pas le cas avant.
* Design revu pour coller au reste de l'app : titres lisibles, favoris en tête, session en cours mise en avant et entourée, actions en petites icônes nettes (favori, renommer, supprimer), et un bouton « tout supprimer sauf favoris ».
* Ouverture plus rapide (la liste ne recharge plus le contenu entier de chaque conversation juste pour l'afficher) et, à l'ouverture d'une session, on arrive directement en bas sur les derniers messages.

## Recherche dans la mémoire

* La recherche de l'IA dans sa mémoire remonte enfin les pages pertinentes quand plusieurs mots sont cherchés. Avant, une petite page qui contenait vraiment les mots cherchés pouvait être noyée par de grosses pages qui répètent un mot courant, au point de ne pas ressortir du tout.
* Le classement privilégie désormais les pages qui contiennent le plus de mots de la recherche, puis pondère par la rareté des mots (un mot rare et précis compte plus qu'un mot omniprésent), avec un bonus quand le mot est dans le nom ou le titre de la page.

## Gestion des machines par l'IA

* Rappel de la 0.11.6 (option à activer dans Mode agent) : l'IA peut voir les postes distants disponibles et basculer elle-même la machine sur laquelle elle agit. Ici, quand elle bascule, l'icône de la barre de saisie se met à jour tout de suite.

> Note : versions interface et mémoire, le moteur d'inférence n'est pas touché (aucun rechargement de modèle à la mise à jour). Vérifiée sur le serveur de Alice (Linux) ; le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
