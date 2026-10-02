La grande nouveauté de cette version est **Jean, un assistant personnel**, proposé en **bêta**. À côté, l'historique est réorganisé et plusieurs corrections touchent le contexte, le travail sur plusieurs appareils et les projets.

> **Jean est une bêta.** Tout n'a pas encore été testé en conditions réelles. Pour un travail sérieux (code, documents, automatisations importantes), il est recommandé de continuer à utiliser le **mode Projet** ou le **mode Rapide**.

## Jean, assistant personnel (bêta)

Jean est un nouveau mode, à côté de Rapide, Projet et Modèle de base. C'est un assistant qui se souvient de l'utilisateur au fil du temps, dans une seule conversation continue.

- **Une mémoire à lui, sur trois niveaux** : un profil court (faits durables : prénom, proches, préférences, habitudes), des fiches (procédures, recettes, guides gardés en entier) et un journal où chaque échange est consigné et peut être recherché plus tard. La fenêtre « Mémoire de Jean » permet de tout consulter, corriger ou supprimer.
- **Une seule conversation, sans fin.** Les anciens échanges sont rangés au fur et à mesure et se rechargent en remontant le fil, sans ralentir l'application. Après 3 heures sans message, le contexte du modèle repart à vide (le fil affiché et le journal restent intacts) ; le délai se règle avec la clé `JEAN_IDLE_HOURS` (0 = jamais).
- **Rappels et tâches** : Jean peut programmer un rappel ou une tâche récurrente, et le résultat arrive comme un message de sa part dans la conversation, avec une notification.
- **Un espace de travail séparé** : Jean a son propre dossier de travail et son propre dossier de scripts. Il ne peut pas modifier les scripts, les fichiers ni la mémoire des projets. Il peut en revanche les **consulter en lecture seule** (par exemple pour voir comment fonctionne un projet), et refaire ou adapter ce dont il a besoin dans son propre espace.
- **Interface plein écran** avec un avatar, et un badge « Beta » dans le menu des modes.

## Historique et projets

- **L'historique suit le mode et le projet.** En mode Projet, il affiche les conversations du projet actif ; en mode Rapide ou Modèle de base, les conversations rapides ; en mode Jean, aucun historique (Jean n'a qu'une conversation). La recherche, elle, couvre toutes les conversations.
- « Tout supprimer » n'efface plus que ce qui est affiché.
- La conversation de Jean n'apparaît plus dans un projet et ne peut plus être effacée par « Tout supprimer » ou par la suppression d'un projet.
- Supprimer un projet n'efface plus les conversations rapides rangées dessous.
- Le menu « ⋯ » d'un projet s'ouvre par-dessus la liste des projets au lieu de la refermer. Il propose désormais **Voir les trackers** (les trackers de ce projet, sans basculer dessus), et le renommage se fait dans **Options**, avec la description.
- Ouvrir une conversation depuis l'historique ne ramène plus au menu latéral.

## Plusieurs appareils

- Un appareil resté en mode Jean pendant qu'un autre ouvrait une conversation de projet envoyait sa question dans ce projet : la réponse n'apparaissait jamais chez Jean. Chaque message part désormais dans la conversation qui correspond au mode de l'appareil, et chaque appareil adopte le mode de la conversation ouverte ailleurs.
- Changer de projet depuis un appareil en mode Jean ne fait plus quitter Jean.

## Contexte et compactage

- Une étape qui lisait plusieurs gros fichiers d'un coup pouvait dépasser la fenêtre de contexte sans déclencher le compactage (« request exceeds the available context size »). Les résultats d'outils d'une étape sont désormais comptés avant l'envoi, chaque résultat est plafonné, et si le moteur refuse malgré tout une requête trop longue, la conversation est réduite puis relancée au lieu d'afficher l'erreur.
- Le compteur « · N compactages » ignorait les compactages faits pendant une réponse, les plus fréquents avec un petit contexte (#102).
- Après un compactage en cours de réponse, le contexte du projet (ou le profil de Jean) est remis en tête de conversation.

## Corrections

- Un identifiant de point de tracker pouvait être attribué deux fois (horloge de Windows), et une modification visait alors le mauvais point.
- Le sélecteur de script d'une tâche propose les scripts du bon espace (projets ou Jean).

## Mise à jour

    ajean update

Vérifié sur le serveur Linux (tests automatiques, interface, menus, historique). Plusieurs points n'ont pas été éprouvés en conditions réelles : Jean dans la durée, son accès en lecture seule aux projets, le vidage du contexte après inactivité, l'usage sur plusieurs appareils simultanés et le téléchargement des fichiers créés par Jean.
