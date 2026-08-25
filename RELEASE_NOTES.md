Ta mémoire et tes conversations peuvent désormais être chiffrées, avec une clé qui ne vit que sur tes appareils et jamais sur le serveur. Et si tu es abonné à ajean.link, tu peux en plus les sauvegarder sur le relais, qui ne voit qu'un paquet opaque.

## Chiffrement de la mémoire et des conversations

* Nouveau réglage dans Paramètres : **activer le chiffrement**. Désactivé par défaut. Une fois activé, tes pages mémoire et tes conversations (fil courant + historique) sont chiffrées au repos sur le disque, en AES-256. Un dossier copié, une sauvegarde ou un disque volé sans ton serveur complet sont illisibles.
* La clé, c'est ta **clé d'API** (celle qui te donne déjà accès à l'interface). Elle ne vit que dans ton navigateur, sur chaque appareil : le serveur n'en garde que l'empreinte, jamais la clé. **Ton serveur seul ne peut pas déchiffrer ta mémoire.**
* Rien à retaper au quotidien : si tu as accès à l'interface, la mémoire s'ouvre toute seule. Sur un nouvel appareil, elle te demande ta clé une fois, puis la retient.
* À l'activation, une **clé de récupération** t'est donnée : note-la, elle rouvre tout même si tu perds ta clé.
* La case n'est cochée que si **tout** est réellement chiffré : si quelque chose n'est pas complet, elle reste décochée, tu es donc sûr de l'état.

## Sécurité, sans rien perdre

* Le passage au chiffrement (et le retour au clair) ne détruit jamais rien : chaque page est relue et vérifiée avant que l'ancienne ne soit retirée, un instantané de sécurité est pris avant chaque bascule, et une migration interrompue se reprend proprement.
* Des instantanés locaux de ta mémoire sont conservés automatiquement, même sans abonnement, pour pouvoir revenir en arrière en cas de pépin.

## Sauvegarde sur ajean.link (abonnés)

* Nouveau bloc **Sauvegarde ajean.link** dans Paramètres, visible si ton serveur est lié à ton compte. Il sauvegarde ta mémoire, tes presets et tes réglages sur le relais.
* Tout est **chiffré sur ton serveur avant l'envoi** : le relais ne stocke qu'un blob opaque, illisible même s'il était piraté. La restauration se fait avec ta clé d'API sur n'importe quel serveur, même vierge.
* Sauvegarde manuelle ou **automatique** une fois par jour, sans mot de passe à gérer. Les 10 dernières versions sont conservées, les plus anciennes tournent automatiquement.

## Sous le capot

* La clé de pilotage n'est plus stockée en clair sur le serveur, seulement son empreinte, et l'accès distant via ajean.link est authentifié par ton identité chiffrée de bout en bout. Aucun changement visible pour toi, l'accès marche comme avant.

> Note : le chiffrement est optionnel et désactivé par défaut, rien ne change si tu ne l'actives pas. Le moteur d'inférence n'est pas touché (aucun rechargement de modèle à la mise à jour). Vérifié sur le serveur de Alice (Linux) et via ajean.link ; le comportement sous macOS n'a pas été testé.

## Mettre à jour

```
ajean update
```
