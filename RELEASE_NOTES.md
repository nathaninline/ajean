Cette version rend la liste des conversations beaucoup plus légère et retravaille entièrement l'affichage des pièces jointes, avec une visionneuse d'images plein écran.

## Liste des conversations

Avec plusieurs centaines de conversations, l'ouverture du menu des projets devenait lente : la liste était affichée en entier d'un coup, et le serveur lisait au passage le contenu complet de toutes les conversations archivées (plus de 250 Mo sur une installation réelle) alors qu'il n'avait besoin que de leurs titres.

La liste affiche désormais les 40 conversations les plus récentes, puis charge les suivantes au fil du défilement. Les favorites restent en tête. Le serveur ne lit plus le contenu des conversations pour construire la liste.

## Pièces jointes

- Les images jointes s'affichent en vraies vignettes : grandes et aux proportions d'origine quand l'image est seule, en rangée de carrés quand un message en contient plusieurs, carrées dans la zone de saisie. Un reflet animé indique le chargement, puis l'image apparaît en fondu.
- Un clic sur une image l'ouvre dans une visionneuse plein écran : l'image s'agrandit depuis sa vignette sur un fond flouté. Navigation entre les images d'un même message (flèches à l'écran ou touches ← →), zoom au double-clic ou à la molette puis déplacement en glissant, téléchargement, fermeture par Échap, clic à côté ou, sur mobile, glissement vers le bas. Les images insérées par l'IA dans ses réponses s'ouvrent de la même façon.
- Les autres fichiers affichent une icône selon leur type (PDF, document, tableur, code, archive, audio, vidéo, texte).
- La zone de saisie ne se reconstruit plus entièrement à chaque étape d'un envoi : les vignettes ne clignotent plus, un anneau de progression s'affiche sur l'image en cours d'envoi, et l'ajout ou le retrait d'un fichier est animé.
- Une image dont le chargement échoue affiche son nom et une icône au lieu de disparaître.

## Mise à jour

    ajean update

Non testé : la version macOS (compilée par la CI, pas vérifiée sur un Mac).
