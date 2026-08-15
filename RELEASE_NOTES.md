Cette version intègre pleinement les tâches planifiées : l'IA peut travailler toute seule, en arrière-plan, sur une fréquence que tu règles, avec un vrai contrôle sur ce qu'elle a le droit de faire.

## Tâches planifiées

Tu crées une tâche en écrivant une consigne en langage naturel et en choisissant sa fréquence. L'IA l'exécute automatiquement, isolée de ta conversation, et agit avec ses propres outils (mail via un serveur MCP, fichiers, web). Le mode agent doit être actif pour qu'une tâche puisse faire quelque chose.

Ce qui est nouveau ou complété dans cette version :

* Fréquence en intervalle simple (toutes les N minutes, heures ou jours) ou en horaire précis (expression cron).
* Choix de l'heure pour les intervalles en jours, par exemple tous les jours à 23h. L'heure est interprétée dans ton fuseau, plus dans celui du serveur, donc fini le décalage.
* Choix du preset (donc du modèle) utilisé par la tâche : le preset actif par défaut, ou un preset précis que la tâche activera avant de s'exécuter.
* Réglage par tâche de l'accès à la mémoire et de l'accès au web, indépendamment l'un de l'autre.
* Activer ou désactiver chaque tâche, ou tout suspendre d'un coup avec un interrupteur maître pour discuter tranquille.
* Bouton pour lancer une tâche à la main et suivre son exécution en direct dans la liste, avec possibilité de l'arrêter.
* Compte-rendu du dernier passage affiché en markdown, avec l'état, l'horodatage à ton heure locale et le temps que la tâche a mis.

## Fonctionnement

Une seule génération tourne à la fois : si tu discutes au moment où une tâche est due, elle attend son tour, et inversement. Une tâche épinglée sur un autre preset recharge le modèle correspondant avant de s'exécuter.

## Mise à jour

```
ajean update
```

Note : le système d'ordonnancement est jeune, teste tes premières tâches sur des fréquences courtes avant de leur confier des créneaux plus longs.
