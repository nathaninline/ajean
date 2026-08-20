Cette version donne à l'IA la capacité de gérer ses propres tâches planifiées et de voir une image du disque, corrige un bug de répartition GPU dans l'éditeur de preset, et nettoie l'interface et le prompt système.

## Tâches planifiées

* L'IA peut désormais gérer ses tâches elle-même : en créer une (rappel, veille, ménage périodique), les lister, les modifier, les activer ou les supprimer. Utile quand quelque chose doit se répéter ou arriver plus tard.
* Le compte-rendu d'une tâche ne contient plus toute la narration intermédiaire de l'exécution, seulement le message final. On récupère le compte-rendu complet d'une tâche précise, plus seulement un aperçu tronqué.
* Dans le menu, la liste des tâches, la pastille du nombre de tâches actives et l'état suspendu se mettent à jour en direct quand l'IA (ou vous) change quelque chose, sans avoir à recharger la page. Un état clair « suspendues » apparaît quand l'interrupteur maître fige tout.

## Vision

* L'IA peut regarder d'elle-même un fichier image du disque (png, jpg, gif, webp, bmp), sans que vous ayez à le joindre. Il suffit de lui donner un chemin. Disponible seulement quand la vision est active sur le modèle.

## Mémoire

* Un outil de suppression permet à l'IA de garder sa mémoire rangée : retirer une page fausse, obsolète ou en double. La consigne l'oriente vers beaucoup de petites pages courtes et logiques plutôt qu'une seule très longue.

## GPU

* Dans l'éditeur de preset, les cartes graphiques pouvaient apparaître dans l'ordre inverse de celui utilisé au lancement du moteur : le curseur de répartition agissait alors sur la mauvaise carte. La liste des cartes suit maintenant exactement l'ordre du moteur en marche.

## Interface

* L'ombre au-dessus de la zone de saisie ne déborde plus sur toute la largeur de la page : elle s'arrête à la largeur de la zone de saisie, sans passer par-dessus la barre de défilement.
* La fenêtre d'édition d'une page mémoire n'a plus de double contour autour du contenu, ce qui récupère de la largeur.
* La vitesse de génération n'est plus répétée sur chaque bulle de raisonnement : elle reste affichée en bas dans la ligne d'état.
* L'animation de compactage du contexte réutilise la même barre d'état avec le logo, qui indique « compactage… » pendant le travail.

## Modèle

* L'assistant s'appelle Jean et fonctionne dans AJEAN. Le prompt système et les descriptions d'outils ont été raccourcis et uniformisés.

## Mettre à jour

```
ajean update
```

Testé sur le serveur de Alice.
