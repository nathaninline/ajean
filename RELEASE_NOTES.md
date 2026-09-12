Correctif du sélecteur de moteur : un modèle configuré sur un moteur personnalisé (un fork de llama.cpp) faisait passer ce fork pour le moteur « compilé » par défaut, sans moyen d'y revenir.

## Corrections

* **Sélection du moteur « compilé ».** Quand un modèle tournait sur un moteur personnalisé installé à côté (un fork de llama.cpp, par exemple une variante à cache d'experts), la carte « compilé » du panneau latéral et l'option « compilé » de l'éditeur de modèle affichaient ce fork au lieu du build par défaut. Choisir « compilé » réécrivait alors le moteur du modèle vers le fork, sans jamais permettre de repasser sur le moteur canonique. En cause : le statut du moteur « compilé » était déduit du moteur du modèle actif, donc il suivait le fork. Le moteur « compilé » désigne désormais toujours le build géré par AJEAN (`backends/llama.cpp`), indépendamment du modèle en cours. La vérification des mises à jour et la recompilation visent elles aussi ce build canonique. Les moteurs personnalisés continuent de se choisir et de se gérer séparément ; un modèle volontairement réglé sur un fork n'est pas modifié.

## Mise à jour

`ajean update` récupère la nouvelle version. Pour un modèle qui était resté accroché à un fork par erreur, rouvrir l'éditeur du modèle, section Moteur, et cocher « compilé » : le moteur repasse sur le build par défaut.
