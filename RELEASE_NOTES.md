Cette version rend AJEAN nettement plus léger et plus fluide sur les longues conversations, en particulier celles qui contiennent des images, et corrige l'interface qui ne répondait plus au toucher pendant les réponses sur mobile.

## Interface qui ne répondait plus pendant une réponse (mobile)

Sur iPhone, pendant que l'IA répondait, les appuis étaient souvent ignorés partout, bouton stop compris, jusqu'à la fin de la réponse. Le fil se recalait en bas toutes les 150 ms, et iOS annule un appui si la position de défilement change pendant qu'il a lieu. Le fil ne se recale plus pendant un contact avec l'écran (il rattrape juste après), et le bouton stop agit dès l'appui sur écran tactile, pour pouvoir interrompre une génération immédiatement.

## Conversations avec images : jusqu'à 100 fois plus légères

Chaque image jointe était stockée en entier, encodée en base64, dans l'historique de la conversation : une conversation de quatorze messages pouvait peser 36 Mo, réécrits à chaque réponse et relus à chaque ouverture. Les images sont désormais rangées une seule fois sur le disque (dossier `chatimg`, hors du dossier de travail de l'IA), et l'historique n'en garde qu'une référence. Le modèle reçoit exactement les mêmes images qu'avant. Les conversations existantes sont converties automatiquement au démarrage, sans perte.

Dans le fil, les vignettes affichaient la photo d'origine en pleine résolution (une photo de 12 mégapixels pour un carré de quelques centaines de pixels), ce qui faisait saccader le défilement. Elles sont maintenant réduites une fois à leur taille d'affichage ; la visionneuse et le téléchargement utilisent toujours l'original. L'ouverture d'une image dans la visionneuse ne saccade plus.

## Ouverture et chargement des conversations

- Au chargement de la page ou à l'ouverture d'une conversation, seuls les 20 derniers échanges sont affichés ; un bouton en haut du fil affiche les précédents sans perdre la position de lecture.
- Le menu des projets se ferme dès le clic sur une conversation, avec un indicateur de chargement, au lieu de rester figé le temps que le serveur réponde.
- Le rendu d'une conversation ne bloque plus le navigateur par à-coups, et seuls les messages visibles à l'écran sont mis en page et dessinés.
- Chaque fin de réponse réécrivait la conversation entière deux fois ; elle n'est plus écrite qu'une fois.

## Stockage

- Les longs résultats d'outils des anciennes conversations sont remplacés dans l'affichage par un aperçu, le texte complet restant accessible par « voir plus ».
- Les résultats complets de « voir plus » sont désormais chiffrés comme les conversations (ils étaient stockés en clair depuis la 0.15.5), rangés avec leur conversation et supprimés avec elle, sans plafond global qui les faisait disparaître sur les anciennes conversations.
- Sur une installation réelle, les conversations archivées sont passées de 252 Mo à 109 Mo.

La base de données ne rend pas d'elle-même l'espace libéré au disque. Pour le récupérer, arrêter AJEAN, puis compacter le fichier `ajean.db` (par exemple avec l'outil `bbolt compact`).

## Divers

- Les réponses du serveur local (interface et données) sont compressées : la page passe de 850 Ko à moins de 200 Ko transférés.
- Les commentaires de développement ne sont plus inclus dans l'interface servie aux utilisateurs.
- Un test dépendant du réseau ne peut plus faire échouer la construction d'une version.

## Mise à jour

    ajean update

Non testé : la version macOS (compilée par la CI, pas vérifiée sur un Mac). La conversion des anciennes conversations s'exécute en arrière-plan une vingtaine de secondes après le démarrage, et seulement une fois la mémoire chiffrée déverrouillée si le chiffrement est actif.
