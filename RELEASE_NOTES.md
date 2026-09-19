Nouvelle fonctionnalité de contrôle du navigateur, qui permet à l'IA de piloter un navigateur web sur la machine hôte, et affichage des images directement dans le chat.

## Contrôle du navigateur

Un nouvel axe de capacité, activable sous le mode agent depuis les réglages, donne à l'IA la possibilité de piloter un navigateur Chrome, Chromium ou Edge installé sur la machine hôte. L'IA ouvre une adresse, lit les éléments interactifs de la page sous forme de liste numérotée (aucune vision requise), puis agit par numéro : cliquer, saisir du texte, choisir dans une liste déroulante, appuyer sur une touche, faire défiler. Elle peut aussi capturer la page, rechercher un élément situé hors de la vue, et cliquer par coordonnées un élément qu'aucun numéro ne couvre (bandeau de consentement dans une iframe, canvas). L'approche par éléments numérotés fonctionne avec de petits modèles et une faible consommation de contexte.

La fonctionnalité s'ajoute aux réglages sous le mode agent et nécessite un navigateur installé sur la machine. Tant qu'elle est désactivée, aucun outil n'est proposé au modèle et aucun navigateur n'est lancé : le comportement reste identique aux versions précédentes.

## Affichage des images dans le chat

Une image du dossier de travail de l'IA, insérée dans sa réponse en syntaxe image Markdown, s'affiche désormais en aperçu directement dans la conversation (capture d'écran, graphique, image générée), au lieu de n'apparaître que sous forme de lien de téléchargement.

## Mise à jour

    ajean update
