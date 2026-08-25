Version corrective importante, autour de la sécurité du chiffrement de la mémoire, des presets, du planificateur et de la répartition multi-GPU.

## Réglages effacés par un changement de preset : correction critique

* Un changement de preset remplace toute la configuration en bloc, ce qui effaçait des réglages utilisateur qui y étaient rangés, donnant l'impression qu'ils « se désactivaient tout seuls ». Ces réglages sont désormais préservés à travers les bascules de preset : le drapeau de chiffrement de la mémoire (MEM_ENCRYPTED), la sauvegarde automatique ajean.link (BACKUP_AUTO), le compactage automatique du contexte (COMPACT) et la gestion autonome des machines (MACHINES).
* Conséquence la plus grave, corrigée : quand le drapeau de chiffrement disparaissait, l'interface croyait le chiffrement désactivé, ne proposait plus de déverrouiller, et la mémoire restait chiffrée mais inaccessible.
* Garde-fou ajouté : activer le chiffrement est refusé si un coffre existe déjà (au lieu d'en forger un nouveau par-dessus, ce qui rendait illisibles les pages chiffrées sous l'ancienne clé). Dans ce cas, il faut déverrouiller la mémoire existante.
* Résilience de l'interface : si le drapeau venait à manquer alors qu'un coffre existe, l'invite de déverrouillage est proposée quand même, au lieu de laisser la mémoire muette sans rien demander.

## Presets

* Modifier le preset actif le désélectionne de nouveau. Le fichier enregistré diverge alors de la configuration qui tourne encore, il faut « switcher » pour l'appliquer. Un simple renommage, ou le raccourci « niveau de réflexion », ne désélectionne pas.
* Éditeur de preset, section Cartes graphiques : nouveau choix du mode de répartition multi-GPU (--split-mode), visible dès deux cartes sélectionnées. Options : automatique, par couches (layer), par rangées (row), par tenseurs (tensor), une seule carte (none).

## Planificateur de tâches

* Réactiver les tâches après une pause ne les relance plus toutes d'un coup. Les échéances tombées pendant la pause sont ignorées, chaque tâche repart à son horaire habituel.

## Chat

* Compteur de compactages du contexte affiché à côté de la jauge, par session.
* Plus de notification de fin de réponse quand la génération est arrêtée volontairement avec le bouton stop.
* Le libellé sous le champ de saisie reflète désormais l'option « Entrée = retour à la ligne » au lieu de rester figé.

## Mettre à jour

```
ajean update
```
