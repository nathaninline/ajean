AJEAN s'ouvre désormais dans sa propre fenêtre, comme une application, au lieu d'un onglet de plus dans le navigateur.

## Interface

- **Fenêtre dédiée** : au lancement (double-clic sur l'application, ou « Ouvrir AJEAN » depuis l'icône de la zone de notification / de la barre de menus), l'interface s'ouvre dans une fenêtre sans onglets ni barre d'adresse, avec sa propre icône dans la barre des tâches ou le Dock. Relancer AJEAN ne multiplie plus les onglets dans le navigateur habituel.
- La fenêtre s'appuie sur un navigateur de la famille Chromium déjà présent sur la machine : Microsoft Edge sous Windows (installé d'office), Chrome, Edge, Brave ou Chromium sous macOS et Linux. Elle utilise un profil séparé, distinct de la navigation de tous les jours. Sans navigateur compatible, AJEAN retombe sur le navigateur par défaut, comme avant.
- Pour revenir à l'ancien comportement (onglet dans le navigateur par défaut) : variable d'environnement `AJEAN_BROWSER=1`.

## Mise à jour

    ajean update

Vérifié sous Windows (fenêtre Edge dédiée). Le comportement sous macOS et Linux n'a pas été testé sur une vraie machine.
