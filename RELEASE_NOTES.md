Ajout en cours de réponse, robustesse multi-appareils et clarté du menu AJEAN LINK.

## Ajouter un message pendant que l'IA répond

Il fallait auparavant arrêter la génération, envoyer sa précision, puis relancer. Désormais un message envoyé pendant une réponse se met en file : il est pris en compte à la prochaine étape de la réponse (par exemple après un appel d'outil), ou traité au tour suivant si la réponse se termine avant. Plusieurs messages peuvent être mis en attente, ils s'affichent en gris jusqu'à leur prise en compte. Le bouton d'envoi reste disponible pendant la génération dès qu'il y a du texte à envoyer.

## Fusion de deux conversations entre appareils

Commencer une conversation sur un appareil, puis en démarrer une autre (ou changer de projet) sur un second appareil, laissait le premier afficher un mélange des deux fils jusqu'à un rafraîchissement manuel. Cause : à la reconnexion, le fil courant était rejoué par-dessus l'ancien sans le remplacer. Le serveur signale maintenant à chaque appareil la conversation réellement active et lui fait vider l'affichage avant de rejouer, ce qui supprime la fusion.

## Mémoire mieux conservée après un compactage

Quand le contexte est compacté, le contenu des pages mémoire lues était résumé, si bien qu'un règlement de tâche pouvait être oublié en cours de route. Un rappel est désormais réinjecté après compactage : il liste les pages déjà consultées et invite à les rouvrir si nécessaire, sans recopier leur contenu, pour ne pas alourdir le contexte.

## Moteur non installé : avertissement clair

Dans l'éditeur de modèle, choisir un moteur non installé (précompilé ou compilé) ne donnait qu'une notification fugace. Chaque option indisponible porte maintenant un repère « non installé », et la sélection affiche un encart expliquant la marche à suivre, avec un bouton qui ouvre la section Moteur et lance l'installation.

## Menu AJEAN LINK

La section met en avant, lorsque le serveur n'est pas connecté, les avantages de l'abonnement : accès distant chiffré de bout en bout, support dédié par chat, sauvegarde intégrée des réglages, presets, historique et mémoire. Une fois connecté, un rappel discret de ces avantages figure en bas de la section.

## Ligne d'état de génération

La ligne sous la réponse indique maintenant le preset utilisé, avec un format plus compact : durée, nombre de tokens, vitesse (t/s) puis nom du preset.

## Mise à jour

```
ajean update
```

Non vérifié en conditions réelles avant publication : la mise en file de messages pendant une génération avec outils, et l'avertissement de moteur non installé dans l'éditeur de modèle.
