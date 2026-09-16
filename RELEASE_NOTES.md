Prise en charge des images avec les modèles distants, retrait de la gestion des postes distants, et corrections autour des pièces jointes.

## Images et modèles multimodaux via une API externe

Un preset « API externe » (modèle servi par une API compatible OpenAI, y compris un autre serveur AJEAN) peut désormais être déclaré multimodal, via une case « le modèle accepte les images » dans sa fenêtre de configuration. Quand elle est cochée, les images jointes à un message sont envoyées au modèle distant pour qu'il les voie, et l'outil de vision (chargement d'une image du disque) lui est proposé. Auparavant, seul un projecteur multimodal local pouvait activer la vision : un modèle distant pourtant capable de voir se voyait refuser les images.

## Images jointes utilisables, pas seulement visibles

Lorsque la vision est active, une image jointe est à la fois montrée au modèle et signalée comme fichier de son dossier de travail, avec son chemin. Le modèle peut donc l'analyser directement et, s'il le faut, agir sur le fichier (le convertir, le recadrer avec ses outils). La distinction est explicite pour éviter que le modèle rouvre inutilement une image qu'il a déjà sous les yeux.

## Redimensionnement des images avant l'envoi au modèle

Une image dont le plus grand côté dépasse 1568 pixels est réduite avant d'être transmise au modèle, à la manière des API de vision courantes. Le projecteur multimodal retaille de toute façon l'image à sa propre résolution : envoyer une définition supérieure ne fait qu'alourdir le transfert et le contexte, sans bénéfice. La réduction se fait par moyennage, et l'orientation issue des métadonnées EXIF (photos de téléphone) reste corrigée.

## Retrait de la gestion des postes distants

La fonctionnalité permettant à l'IA d'un serveur de piloter un autre PC (postes distants) a été entièrement retirée : commandes, réglages, outils, interface et documentation associés. L'accès à distance à une machine passe désormais uniquement par sa connexion à ajean.link.

## Téléchargement des fichiers au nom contenant une apostrophe

Un fichier renvoyé par l'IA dont le nom comportait à la fois des espaces et une apostrophe (par exemple « Capture d'écran … ») produisait un lien de téléchargement invalide. La détection d'un éventuel titre de lien a été resserrée pour ne plus confondre une apostrophe interne au nom avec la syntaxe d'un titre.

## Mise à jour

    ajean update
