Cette version corrige une faille de sécurité de l'interface locale, fiabilise la mise à jour et le bouton « voir plus », et retire le mode CODER.

## Sécurité : l'interface locale ne peut plus être pilotée par un site tiers

Sans clé de pilotage (le réglage par défaut), une page web ouverte dans le navigateur de la machine pouvait envoyer en arrière-plan des requêtes à l'interface locale d'AJEAN et la piloter : changer des réglages, et, mode agent activé, faire exécuter des commandes par l'IA. Le navigateur laissait passer ces requêtes car rien ne vérifiait leur provenance.

L'API refuse désormais toute requête qui vient d'un autre site. Sans clé de pilotage, l'interface n'est plus accessible que par une adresse locale (IP, localhost, nom de la machine, noms en .local ou .lan), ce qui bloque aussi l'attaque dite de « DNS rebinding ». L'interface locale, l'accès depuis le réseau par IP, app.ajean.link, l'API compatible OpenAI et les scripts fonctionnent comme avant. Pour accéder à l'interface par un nom de domaine, il faut définir une clé de pilotage (`ajean set-web-key`).

## Mise à jour : fin de l'erreur de somme de contrôle

La mise à jour pouvait échouer avec « somme SHA-256 invalide », alors que les fichiers publiés étaient corrects. Deux mises à jour lancées en même temps (un second clic pendant un téléchargement lent, ou l'interface ouverte à la fois en local et sur app.ajean.link) écrivaient dans le même fichier temporaire, et le binaire obtenu mélangeait les deux téléchargements.

Une seule mise à jour peut maintenant tourner à la fois, chaque téléchargement utilise son propre fichier temporaire, et un téléchargement abîmé en route est recommencé une fois automatiquement avant d'afficher une erreur. Les fichiers temporaires laissés par une mise à jour interrompue sont nettoyés au lancement suivant.

## Résultats d'outils

Le bouton « voir plus » ne dépliait parfois rien, surtout sur les commandes du terminal. Le résultat complet n'était cherché que dans la conversation vue par le modèle, où il n'apparaît qu'à la fin du tour, et d'où il disparaît après un compactage. Les résultats complets sont désormais enregistrés à part et se déplient pendant un tour en cours, après un compactage ou dans une session rouverte. En cas d'échec, un message l'indique au lieu d'un bouton inerte.

Les commandes du terminal affichaient toujours environ 2004 tokens : leur sortie est limitée à 8000 caractères, et la coupe était silencieuse. Une sortie coupée commence maintenant par une mention qui indique sa taille réelle, pour l'utilisateur comme pour le modèle.

## Mode agent

- Le compactage de secours résumait la conversation à la moindre erreur du moteur (appel d'outil mal formé, modèle en cours de chargement), même quand elle était courte. Il ne se déclenche plus que sur un vrai dépassement de la fenêtre de contexte.
- Un appel d'outil dont les arguments sont illisibles (JSON tronqué) n'est plus exécuté avec des arguments vides, ce qui produisait des erreurs trompeuses. Le modèle est prévenu et renvoie un appel complet.
- Quand les outils sont coupés après une erreur, la réponse de secours se fait dans la langue de l'utilisateur, et non plus toujours en français.

## Mode CODER retiré

Le mode CODER (option par projet) est supprimé, ainsi que son badge au-dessus de la zone de saisie. Les options d'un projet ne contiennent plus que sa description. Les projets où il était activé n'ont rien à faire.

## Maintenance

Le code de reprise des installations 0.7 est retiré. Une installation 0.7 qui passerait directement à cette version devrait déplacer ses dossiers à la main.

## Mise à jour

    ajean update

Non testé : la version macOS (compilée par la CI, pas vérifiée sur un Mac). La cause de l'erreur de somme de contrôle a été établie par lecture du code et de la release 0.15.4 (dont les fichiers sont corrects), sans reproduction chez l'utilisateur concerné.
