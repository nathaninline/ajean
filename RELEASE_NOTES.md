Compatibilité avec les versions récentes de llama.cpp, dont les options de chargement mémoire ont changé, une commande de rotation de l'identifiant de machine pour l'accès distant, et plusieurs améliorations et corrections d'interface.

## Nouveautés

* **Compatibilité llama.cpp récent (`--load-mode`).** Les versions récentes de llama.cpp ont remplacé les options `--mlock` et `--no-mmap` par une option unique `--load-mode`, et rejettent désormais les anciennes. AJEAN détecte au lancement la syntaxe acceptée par le moteur et traduit automatiquement les anciens drapeaux : un même preset reste lançable aussi bien sur un moteur récent que sur un moteur ancien ou un fork, sans modification. Lorsque d'anciens drapeaux sont détectés sur un moteur récent, l'interface propose un bouton pour mettre la configuration à jour, et un message explicite est affiché si un moteur refuse de démarrer pour cette raison.
* **Rotation de l'identifiant de machine.** Nouvelle commande `ajean link newid` : elle attribue un nouvel identifiant à la machine et rouvre le tunnel d'accès distant, utile lorsque l'ancien identifiant a été exposé publiquement.

## Interface

* Le panneau Projets a été retravaillé : dossiers plus compacts, section « Historique » nettement séparée de la liste des projets, et bouton « Nouvelle conversation » placé à droite de l'en-tête, en bouton d'accent bien visible.
* Le menu d'accès distant s'intitule désormais « AJEAN LINK ».
* Les interrupteurs « Garder en RAM » et « Charger tout en mémoire » reflètent correctement leur état même lorsque la configuration utilise la nouvelle syntaxe `--load-mode`.

## Corrections

* La déconnexion de l'accès distant depuis l'interface répond désormais correctement, sans erreur, et laisse l'interface locale active au lieu de couper tout le service.
* L'indicateur d'activité ne reste plus affiché indéfiniment lorsqu'une génération est interrompue côté serveur.

## Mise à jour

```
ajean update
```
