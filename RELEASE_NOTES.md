L'IA peut maintenant gérer ses machines toute seule (voir les postes, basculer dessus), et tu peux mettre tes conversations en favori et les renommer dans l'historique.

## Interface

* Historique des conversations : chaque conversation peut être mise en **favori** (elle remonte en tête de liste, repérée par une étoile) et **renommée** avec un nom personnalisé. Les deux se règlent depuis un seul écran, le bouton « renommer » ouvre le champ du nom et la case favori.
* Nouveau bouton **« tout supprimer (sauf favoris) »** dans l'historique : il vide toutes les conversations archivées d'un coup, en gardant celles que tu as épinglées.
* Les boutons de l'historique sont passés en petites icônes (favori, recharger, renommer, supprimer) pour ne plus déborder de la ligne, avec une infobulle au survol.
* Quand l'IA change de machine elle-même, l'icône de la barre de saisie et le sélecteur de cible se mettent à jour tout de suite, sans avoir à cliquer.

## Gestion des machines par l'IA (nouveau, désactivé par défaut)

* Nouvelle option dans **Mode agent, Paramètres** : la **gestion autonome des machines**. Activée, elle donne à l'IA deux outils pour voir les postes distants disponibles et basculer la machine sur laquelle elle agit, et la rend consciente de la procédure pour en ajouter un elle-même (via ssh et `ajean remote install`).
* Désactivée par défaut : tant que la case n'est pas cochée, rien ne change, ni le comportement de l'IA ni ses instructions.
* Commande d'appoint `ajean postes pair` pour générer un code d'appairage en ligne de commande.

> Note : la gestion des machines n'est pas activée d'office, il faut la cocher soi-même. Vérifiée sur le serveur de Alice (Linux) ; le comportement sous macOS n'a pas été testé. À savoir : quand l'IA bascule de machine, le contexte est recalculé en entier (comportement inchangé du changement de cible).

## Mettre à jour

```
ajean update
```
