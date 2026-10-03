Jean (bêta) apprend de ses échanges, et plusieurs correctifs du mode agent.

## Jean (bêta) : une mémoire qui apprend

Le mode Jean reste en bêta, mais cette version l'améliore en profondeur : il approche de sa sortie en version normale.

- **Révision en arrière-plan.** Après quelques minutes d'inactivité, Jean relit les derniers échanges et retient ce qui durera : un fait sur l'utilisateur dans le profil, une leçon après une correction ou un échec, une procédure dans une fiche. La plupart du temps il n'y a rien à retenir, et rien n'est écrit.
- **Leçons.** Quand l'utilisateur le corrige, Jean garde la règle et ne refait plus l'erreur. Une leçon qui concerne une tâche précise va dans la fiche de cette tâche.
- **Règles appliquées.** Une consigne d'écriture mécanique (un caractère interdit, pas de liste numérotée, un nombre maximal d'éléments) est appliquée par AJEAN à tout ce que Jean écrit, sans dépendre de sa mémoire.
- **Ménage nocturne.** Les doublons sont fusionnés et chaque information est rangée à sa place. Un contrôle sans IA vérifie ensuite que rien de ce qui a été appris ne s'est perdu ; sinon, la ligne est restaurée.
- **Fiches archivées au lieu d'être supprimées.** Une fiche qui ne sert plus sort de la liste mais reste retrouvable.
- **Garde-fous.** Aucun mot de passe, clé ou jeton n'entre dans le profil (relu à chaque message) : ils restent dans la fiche du service concerné. Une leçon déjà écrite n'est pas recopiée, et un piège rangé dans la mauvaise fiche est signalé.
- **Consignes revues et allégées.** Jean répond d'abord à la question posée, dans la langue de l'utilisateur, vérifie sur internet quand il n'est pas sûr d'un fait, croit l'utilisateur quand celui-ci corrige sa lecture d'une photo, et demande avant de modifier le système. Le préambule envoyé au modèle est environ 28 % plus court.
- Jean range sa mémoire avant de répondre et ne répète plus sa réponse après l'avoir fait.
- Les pièces jointes et les captures du navigateur arrivent dans le dossier de travail de Jean, qui les trouve directement.

## Agent

- **Recherche d'images** : nouvel outil `web_images`, sur le même principe que la recherche web. Les images du web s'affichent dans le chat et s'agrandissent au toucher.
- **Zoom du navigateur piloté** : `browser_screenshot` peut capturer une zone de la page en haute définition (un détail lointain sur une webcam, un texte petit).
- Le navigateur piloté se ferme désormais avec AJEAN, même après un arrêt brutal, sous Windows et Linux. Auparavant, chaque redémarrage pouvait laisser un Chrome caché tourner en arrière-plan et charger le processeur.
- Le navigateur piloté se ferme après 10 minutes sans usage (il est relancé au besoin) : une page animée restée ouverte, comme une webcam, ne charge plus le processeur indéfiniment. Sa fermeture arrête aussi tous ses sous-processus (#109).
- Un numéro d'élément introuvable dans le navigateur renvoie la liste à jour au lieu d'un message qui tournait en rond.
- Mode Jean avec une API externe stricte (DeepSeek…) : chaque message était refusé à cause d'un schéma d'outil invalide (#106).
- Correction d'un plantage possible de l'interface lors de la fermeture d'une connexion en direct (#105).
- Les outils de rappel de l'historique ne sont proposés qu'après un compactage, et les trois outils de lecture des projets sont réunis en un seul.

## Mise à jour

    ajean update

La mémoire existante de Jean est conservée telle quelle : les nouveaux fichiers (leçons, règles) sont créés au premier besoin.

Vérifié sur une instance de test Windows (tests automatiques, nombreux échanges réels avec Jean : mémoire, webcam, recherche d'images, poster, arrêt forcé du serveur). Non éprouvé sur le serveur Linux ni sur macOS, où la fermeture automatique du navigateur n'existe pas.
