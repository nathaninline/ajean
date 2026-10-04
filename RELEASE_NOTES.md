Mémoire de Jean mieux rangée et nettoyage après la désactivation du chiffrement.

## Mode Jean

- **Valeurs périmées pendant le rangement nocturne.** Quand une information changeait (une durée, un rythme, un chemin), la consolidation de la mémoire pouvait ajouter la nouvelle valeur à côté de l'ancienne au lieu de la remplacer, ce qui laissait des fiches contradictoires (par exemple deux durées différentes pour le même envoi). La consigne demande désormais de remplacer l'ancienne valeur et de vérifier que le reste de la ligne reste cohérent.

## Mémoire

- **Copies chiffrées devenues inutiles.** Après la désactivation du chiffrement de la mémoire, les copies de sécurité `.bak` de l'ancienne version restaient chiffrées alors que la clé avait été retirée : illisibles pour toujours, elles encombraient le dossier de la mémoire. Elles sont retirées à la fin de la désactivation, et au démarrage pour les installations où le chiffrement a déjà été désactivé. Les copies en clair, les sauvegardes et les valeurs mises en quarantaine ne sont pas touchées.

## Mise à jour

    ajean update

Vérifié par les tests automatiques et sur le serveur Linux. L'effet de la nouvelle consigne se verra à la prochaine consolidation nocturne.
