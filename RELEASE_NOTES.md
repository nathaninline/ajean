La mémoire et les conversations peuvent désormais être chiffrées, avec une clé qui ne vit que sur les appareils du client et jamais sur le serveur. Pour les abonnés ajean.link, une sauvegarde chiffrée sur le relais est également disponible, le relais ne voyant qu'un paquet opaque.

## Chiffrement de la mémoire et des conversations

* Nouveau réglage dans Paramètres : **activer le chiffrement**. Désactivé par défaut. Une fois activé, les pages mémoire et les conversations (fil courant et historique) sont chiffrées au repos sur le disque, en AES-256.
* La clé est la **clé d'API** (celle qui donne déjà accès à l'interface). Elle ne vit que dans le navigateur, sur chaque appareil : le serveur n'en garde que l'empreinte, jamais la clé. Conséquence : **même une copie complète du serveur (fichiers chiffrés, base, coffre) reste illisible** sans cette clé. Un dossier copié, une sauvegarde ou un disque volé sont donc inexploitables. Seul quelqu'un disposant de la clé d'API (ou de la clé de récupération), qui n'existe que sur les appareils du client, peut déchiffrer.
* Rien à ressaisir au quotidien : avoir accès à l'interface suffit à ouvrir la mémoire. Sur un nouvel appareil, la clé est demandée une fois, puis mémorisée.
* Une **clé de récupération** est fournie à l'activation : à conserver, elle rouvre tout en cas de perte de la clé.
* La case n'est cochée que si **tout** est réellement chiffré : un état incomplet la laisse décochée, ce qui rend l'état fiable d'un coup d'œil.

## Sécurité, sans perte de données

* Le passage au chiffrement (et le retour au clair) ne détruit jamais rien : chaque page est relue et vérifiée avant que l'ancienne ne soit retirée, un instantané de sécurité est pris avant chaque bascule, et une migration interrompue se reprend proprement.
* Des instantanés locaux de la mémoire sont conservés automatiquement, même sans abonnement, pour permettre un retour en arrière en cas de problème.

## Sauvegarde sur ajean.link (abonnés)

* Nouveau bloc **Sauvegarde ajean.link** dans Paramètres, visible lorsque le serveur est lié au compte. Il sauvegarde la mémoire, les presets et les réglages sur le relais.
* Tout est **chiffré sur le serveur avant l'envoi** : le relais ne stocke qu'un blob opaque, illisible même en cas de piratage. La restauration se fait avec la clé d'API, sur n'importe quel serveur, même vierge.
* Sauvegarde manuelle ou **automatique** une fois par jour, sans mot de passe à gérer. Les 10 dernières versions sont conservées, les plus anciennes tournent automatiquement.

## Sous le capot

* La clé de pilotage n'est plus stockée en clair sur le serveur, seulement son empreinte, et l'accès distant via ajean.link est authentifié par l'identité chiffrée de bout en bout. Aucun changement visible : l'accès fonctionne comme avant.

> Note : le chiffrement est optionnel et désactivé par défaut, rien ne change s'il n'est pas activé. Le moteur d'inférence n'est pas touché (aucun rechargement de modèle à la mise à jour). Vérifié sur le serveur de Alice (Linux) et via ajean.link ; le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
