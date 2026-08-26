Version qui ajoute un dossier de scripts dédié et des tâches planifiées de type script (sans IA), renforce la protection de la mémoire, et corrige le déverrouillage de la mémoire chiffrée après un redémarrage.

## Dossier de scripts dédié

* Nouveau dossier `scripts` dans les données d'AJEAN, à côté de la mémoire et des presets, distinct du dossier de travail de l'agent. Ce dernier est un bac à sable jetable où un clone ou un test peut tout effacer ; les scripts destinés à être conservés vivent désormais dans `scripts`, à l'abri d'un nettoyage du dossier de travail. Le nouvel emplacement apparaît dans `ajean where`.

## Tâches planifiées de type script

* Une tâche planifiée peut désormais être de deux types : une consigne exécutée par l'IA, ou un script lancé directement, sans charger le modèle ni consommer de jetons. Le type se choisit dans le formulaire de tâche (Consigne IA ou Script seul), avec sélection du script à lancer.
* Une tâche script en cours d'exécution s'affiche comme active dans la liste des tâches et peut y être arrêtée.

## Protection de la mémoire et des dossiers

* Le dossier de la mémoire n'est plus accessible directement par le shell ni par les outils d'écriture de l'IA : il passe exclusivement par les outils de mémoire dédiés. Cela évite les accès manuels erronés au profit des opérations prévues.
* Garde-fou contre la suppression accidentelle : une commande destructrice visant en bloc la mémoire, les presets, la base, la racine des données ou le dossier scripts est refusée. La suppression d'un fichier précis et le vidage du dossier de travail restent possibles.

## Mémoire chiffrée : déverrouillage après un redémarrage

* Après un redémarrage du serveur, une mémoire chiffrée se reverrouille, la clé ne vivant qu'en mémoire vive. L'interface détecte désormais le redémarrage et relance le déverrouillage automatique avec la clé enregistrée, sans qu'il soit nécessaire de recharger la page.

## Mettre à jour

```
ajean update
```
