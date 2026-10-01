Connexion distante plus robuste, corrections du chat, de la bascule de modèle et du compactage, retouches d'interface.

## Connexion et API externe

- **Reprise automatique après une coupure.** Avec un preset API externe ou GPU cloud, une réponse interrompue par une coupure réseau (tunnel ajean.link qui se reconnecte, Wi-Fi ou VPN qui décroche) n'abandonne plus le tour. Si rien n'était encore affiché, la requête est relancée ; si la réponse avait commencé, le modèle reçoit ce qu'il avait déjà écrit et reprend là où il s'était arrêté. Jusqu'à 5 tentatives, avec un délai croissant.
- Une erreur 502/503 du relais pendant une reconnexion est relancée au lieu de désactiver les outils du mode agent.
- Une erreur définitive (adresse introuvable, connexion refusée, certificat invalide) s'affiche tout de suite.
- La cause d'une coupure du tunnel est désormais journalisée côté serveur (`[link] lien coupé après …`).

## Chat

- Le raisonnement ne semble plus s'interrompre puis reprendre : une réflexion qui reprend après le début de la réponse s'affiche dans une nouvelle bulle à sa place, et une reconnexion du flux n'affiche plus de texte tronqué.
- Un message mis en file pendant une génération ne réapparaît plus dans une nouvelle conversation.
- Le texte écrit avant un appel d'outil n'est plus enregistré deux fois dans l'historique transmis au modèle.
- Deux appareils qui envoient un message au même moment : le second est mis en file au lieu d'être refusé.

## Modèles et compactage

- **Bascule de preset par identifiant** et non plus par position dans la liste : une liste modifiée depuis un autre appareil ne peut plus charger un autre preset que celui choisi.
- Après un compactage, une demande déjà traitée n'est plus réinjectée (le modèle y répondait une seconde fois), et la demande en cours n'est plus dupliquée quand le résumé échoue.

## Interface

- Après une mise à jour sous Windows, la fenêtre AJEAN déjà ouverte est réutilisée au lieu d'en ouvrir une seconde.
- Le bouton « + » et le sélecteur de mode retrouvent un fond gris visible en mode clair, et exactement le même gris l'un que l'autre en mode clair comme en mode sombre.
- « Compacter le contexte » apparaît en tête du menu « + ».

## Mise à jour

    ajean update

Vérifié sur le serveur Linux (interface, couleurs, flux du moteur). La reprise après coupure et la fenêtre unique après mise à jour sous Windows n'ont pas été reproduites en conditions réelles.
