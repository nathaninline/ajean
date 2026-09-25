Cette version rend le GPU Cloud équivalent à un GPU local : l'API OpenAI fonctionne de la même façon, l'état réel du GPU est affiché en direct, et la vision est prise en charge.

## L'API fonctionne avec le GPU Cloud

Avec un preset GPU Cloud, l'API OpenAI d'AJEAN restait muette : elle relayait vers le moteur local, arrêté dans ce mode. Le service moteur lance désormais un relais sur le même port (8080 par défaut) vers le GPU Modal. Tous les accès continuent donc de fonctionner comme avec un GPU local, sans changer la configuration des clients : réseau local, endpoint public `<machine>.oai.ajean.link`, tunnel ajean.link.

- Même règle d'accès que llama-server : la clé API de la machine est exigée. La clé du GPU Cloud n'est jamais communiquée aux clients.
- Réponses en flux (streaming) relayées au fil de l'eau.
- `/health` répond sans réveiller le GPU. Une requête qui arrive pendant le réveil attend que le modèle soit chargé au lieu d'échouer.

## État du GPU Cloud en direct

« Prêt » signifiait seulement « déployé », même quand le GPU était éteint ou en train de télécharger le modèle. L'état réel est maintenant affiché partout :

- **Pastille de statut** : en veille, déploiement, téléchargement, chargement.
- **Carte « Appareil »** : barre de progression pendant le téléchargement du modèle au premier démarrage, puis la VRAM avec un **compte à rebours en direct** avant la mise en veille (« s'éteint dans 4:09 »), « génération en cours » pendant une réponse. Les requêtes passées par l'API sont prises en compte.
- **Chat** : la ligne d'activité indique « réveil du GPU Cloud… » ou « téléchargement du modèle, premier démarrage (plusieurs minutes)… » tant que le modèle n'a pas répondu.
- **Changement de preset** : la carte reste visible tant que le GPU tourne encore (il reste facturé jusqu'à sa mise en veille), puis disparaît.
- En revenant sur l'onglet, la carte n'affiche plus « en veille » à tort pendant quelques secondes.

## Vision

Un preset GPU Cloud accepte un projecteur vision : un champ « Vision » reçoit le lien direct vers le mmproj .gguf, téléchargé une fois côté Modal comme le modèle. Le preset accepte alors les images jointes et affiche l'œil dans la liste des presets.

## Corrections

- **Benchmark fantôme** : un preset nouvellement créé pouvait afficher les mesures d'un autre. Les benchmarks étaient rangés par nom de preset : un preset recréé sous un ancien nom héritait de mesures faites sur un autre modèle. Un benchmark ne s'affiche plus que pour le modèle sur lequel il a été mesuré, il est supprimé avec son preset, et il n'est plus enregistré pour un preset distant (GPU Cloud, API Externe), où il mesurait le moteur local.

## Mise à jour

    ajean update

Vérifié sous Windows et sur un serveur Linux, dont l'API relayée vers le GPU Cloud (réseau local et endpoint public, réponse complète et streaming). Non testé en conditions réelles : la barre de téléchargement pendant un vrai premier démarrage, et l'envoi d'une image à un preset GPU Cloud avec vision.
