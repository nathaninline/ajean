Cette version ajoute les tâches planifiées : l'IA peut travailler toute seule, en arrière-plan, sur une fréquence que tu règles.

## Tâches planifiées

Tu peux maintenant créer des tâches que l'IA exécute automatiquement, sans toi. Par exemple : toutes les 2 heures, regarder tes nouveaux mails et te préparer des brouillons de réponse. Chaque tâche est une consigne en langage naturel plus une fréquence.

Ce qu'on peut faire :

* Régler la fréquence en intervalle simple (toutes les N minutes, heures ou jours) ou en horaire précis (expression cron, ex. tous les jours ouvrés à 9h).
* Activer ou désactiver chaque tâche individuellement.
* Suspendre toutes les tâches d'un coup avec un interrupteur maître, pratique quand tu veux juste discuter sans être dérangé.
* Lancer une tâche à la main pour la tester, et voir son dernier compte-rendu (rendu en markdown) directement dans l'interface.

Comment ça marche : une tâche tourne isolée de ta conversation (elle ne pollue pas le fil), avec les mêmes outils que le mode agent. C'est l'IA elle-même qui agit avec ses outils (mail via un serveur MCP, shell, accès web). Le mode agent doit donc être actif pour qu'une tâche puisse faire quoi que ce soit, et ta consigne doit dire explicitement quoi faire du résultat.

Une seule génération tourne à la fois : si tu discutes au moment où une tâche est due, elle attend son tour, et inversement tu peux arrêter une tâche en cours depuis la liste ou depuis le chat.

## Mise à jour

```
ajean update
```

Note : le système d'ordonnancement est nouveau, teste tes premières tâches sur des fréquences courtes avant de leur faire confiance sur des créneaux plus longs.
