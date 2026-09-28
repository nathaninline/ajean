Version corrective : les presets distants (API externe, GPU Cloud) ne bloquent plus les commandes du moteur et ne font plus perdre le chemin de llama.cpp, et un changement de preset en pleine réponse ne reste plus bloqué une minute et demie.

## Corrections

- **Changement de preset bloqué 90 secondes** : llama-server ignore la demande d'arrêt tant qu'il écrit une réponse. Un changement de preset (ou un redémarrage) pendant une génération attendait donc le délai par défaut de systemd, 90 secondes, avant que le moteur soit arrêté de force ; pendant ce temps, l'interface semblait ne plus répondre et les clics suivants s'empilaient. Le moteur est désormais arrêté au bout de 5 secondes au maximum (il n'a rien à sauvegarder). Mesuré : 1 s au repos, 5 s en pleine génération, contre 91 s auparavant.
- **`ajean start` et `ajean restart` refusés sur un preset distant** : la vérification préalable exigeait `BIN` et `MODEL` même pour un preset API externe ou GPU Cloud, qui n'en ont pas besoin, et invitait à tort à réinstaller llama.cpp. Elle est ignorée pour ces presets (issue #95).
- **Chemin de llama.cpp perdu après une bascule vers un preset externe** : un preset externe ne contenait pas les réglages de la machine (`BIN`, `HOST`, `PORT`). Sur une installation sans preset local, basculer dessus effaçait `BIN`, que plus rien ne contenait ensuite : la seule issue était de réinstaller llama.cpp. Ces réglages sont désormais enregistrés dans le preset externe, à la création comme à la modification.

Merci à @Olioli4 pour les deux dernières corrections (#97).

## Mise à jour

    ajean update

Le délai d'arrêt de 5 secondes s'applique aux nouvelles installations (unité systemd écrite par `sudo ajean install`). Sur une installation existante, il peut être ajouté sans toucher à l'unité :

    sudo mkdir -p /etc/systemd/system/ajean-engine.service.d
    printf '[Service]\nTimeoutStopSec=5\n' | sudo tee /etc/systemd/system/ajean-engine.service.d/stop-timeout.conf
    sudo systemctl daemon-reload

Vérifié sur un serveur Linux (délai d'arrêt mesuré en génération et au repos, bascules entre presets locaux et moteur tiers). Les deux corrections des presets distants sont couvertes par des tests automatiques ; elles n'ont pas été rejouées sur une installation neuve.
