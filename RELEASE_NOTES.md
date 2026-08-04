## Lancement sous Windows

Suite de la 0.6.11, où lancer le fichier téléchargé alors qu'AJEAN tournait déjà se contentait d'ouvrir l'application sans rien mettre à jour.

**AJEAN en cours d'exécution.** Le nouveau binaire était bien écrit sur le disque, mais l'application affichée restait l'ancienne, toujours en mémoire. Rien ne changeait à l'écran, et rien ne le disait. AJEAN demande maintenant s'il faut le fermer et le redémarrer pour appliquer la mise à jour, en affichant la version en cours et celle du fichier lancé. Si vous refusez, l'application s'ouvre telle quelle, et vous savez qu'aucune mise à jour n'a été appliquée.

**Version installée plus récente que le fichier lancé.** AJEAN vous en avertit, ne remplace rien, et vous laisse choisir entre démarrer l'application ou fermer. Auparavant, une version plus ancienne pouvait prendre la place de la plus récente sans un mot.

Le cas courant reste silencieux : fichier plus récent, application arrêtée, le binaire est remplacé et AJEAN démarre.

**Raccourcis.** Ils sont vérifiés à chaque démarrage et recréés s'ils manquent, au lieu d'être posés une seule fois le jour de l'installation. Le raccourci du menu Démarrer est par ailleurs rangé dans « Programmes », l'endroit où Windows classe les applications installées et où va chercher la recherche du menu Démarrer. Il se trouvait jusqu'ici à la racine du menu, donc difficilement trouvable.

## Mise à jour

```
jean update
```

Sous Windows, télécharger le fichier depuis la page des releases et le lancer fait le même travail.

Ces trois comportements demandent une machine Windows avec une installation existante pour être vérifiés en conditions réelles. Les mécanismes qu'ils utilisent (lecture de la version d'un binaire, repérage des instances en cours par leur chemin, remplacement du fichier) sont couverts par des tests, mais l'enchaînement complet des fenêtres n'a pas été parcouru à la main.

L'icône de la barre de menus macOS, introduite en 0.6.10, n'a toujours pas été vérifiée sur une vraie machine.
