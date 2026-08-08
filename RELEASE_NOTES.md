Une version d'audit. Aucune fonctionnalité nouvelle : une relecture du cœur du code, et la correction de ce qu'elle a mis au jour. Deux de ces défauts pouvaient vous mordre pour de bon.

## Le mode agent redevient un vrai interrupteur

L'API de chat acceptait, dans le corps de la requête, une surcharge qui **rallumait** le mode agent. Autrement dit : agent éteint sur la machine, mais un client qui envoyait le bon drapeau récupérait quand même le shell, l'écriture de fichiers et les outils MCP. Comme l'API de pilotage n'est pas protégée par défaut et écoute sur toutes les interfaces, l'interrupteur ne garantissait rien sur un réseau local partagé.

Désormais une surcharge ne peut que **restreindre**. Ce qui est éteint sur la machine ne peut plus être rallumé par une requête, et couper l'agent coupe aussi, du même geste, l'accès web qui en dépend. Deux tests verrouillent la règle dans les deux sens.

Le champ hérité qui portait cette surcharge venait du temps où l'ancien portail gérait ses propres interrupteurs. Il reste accepté, mais borné.

## Une base illisible ne désarme plus l'authentification

Quand aucune clé de pilotage n'est enregistrée, l'API est ouverte : c'est le confort du local. Mais la lecture de cette clé traitait « je n'ai pas pu lire la base » exactement comme « il n'y a pas de clé ». Une base momentanément verrouillée par une commande lancée à côté suffisait donc, en théorie, à ouvrir l'API le temps de la contention.

La lecture des secrets distingue maintenant les deux cas, et l'authentification **ferme** en cas de doute (503) au lieu d'ouvrir.

## Le bouton d'envoi ne peut plus rester bloqué

Avant chaque tour, AJEAN vérifie que le moteur répond. Cet appel n'avait aucun délai maximum. Un moteur qui accepte la connexion sans jamais répondre, ce qui est exactement ce que fait un très gros modèle pendant son chargement, laissait donc l'envoi du message suspendu indéfiniment, sans erreur et sans retour. Il abandonne maintenant au bout de trois secondes et vous dit que le modèle n'est pas prêt.

## L'accès distant ne peut plus se dédoubler

Redémarrer le lien depuis l'interface arrêtait la boucle de connexion, mais la session en cours, elle, continuait de vivre : elle n'écoutait aucun signal d'arrêt et attendait la mort naturelle du WebSocket, qui n'arrive pas tant que le relais répond. Le nouveau tunnel s'ouvrait donc pendant que l'ancien tenait encore, et le relais voyait deux agents pour une seule machine.

L'arrêt est maintenant immédiat et attendu : une session se ferme quand on le lui demande, et la suivante ne démarre qu'après.

Dans la foulée, le délai entre deux tentatives de reconnexion se remet à zéro après une session qui a tenu. Il grimpait sans jamais redescendre et finissait collé à trente secondes, y compris pour rattraper un lien qui venait de fonctionner des heures.

## Le flux de chat ne relit plus tout le fil à chaque mot

Pour envoyer les nouveaux événements à votre navigateur, le serveur reparcourait la totalité du journal de conversation, à chaque token généré, et pour chaque appareil connecté, en tenant le verrou que la génération elle-même attend. Sur une longue conversation, c'est un coût qui grandit avec l'historique et qui ralentit ce qu'il est censé diffuser.

Le journal étant trié, la recherche du premier événement neuf se fait par dichotomie. Le coût ne dépend plus de la longueur de la conversation.

## La configuration n'est plus relue depuis le disque cent fois par tour

La base de données est délibérément fermée entre deux opérations : c'est ce qui permet aux commandes du terminal de fonctionner pendant que le service tourne. Mais la boucle d'inférence relisait le port, la clé et le seuil de compactage à chaque itération, et le compactage se re-testait après chaque appel d'outil. Un tour agentique un peu fourni rouvrait le fichier une centaine de fois.

Un cache de lecture s'interpose, invalidé par toute écriture locale, par une écriture venue d'un autre processus (date et taille du fichier) et, en dernier filet, par l'âge. Le comportement ne change pas d'un iota, le travail inutile disparaît.

## Le reste

Les erreurs du moteur sont classées sur leur type plutôt que sur le texte du message : « connexion refusée » d'un Windows en français ne ressemblait à aucun des motifs anglais reconnus, et l'utilisateur recevait alors l'erreur brute au lieu de l'explication.

Les trois compactions (début de tour, fin de tour, bouton manuel) déroulaient la même douzaine de lignes recopiées ; elles partagent maintenant un seul chemin, ce qui ferme la porte aux corrections qui n'atterrissaient que dans une des trois copies. La ligne de journal de la compaction de secours comparait le résultat à lui-même, elle compare enfin l'avant et l'après. Deux champs de l'API de chat que plus personne ne lisait depuis que la conversation vit côté serveur ont été retirés. Les serveurs HTTP posent un délai de lecture des en-têtes, pour qu'une connexion qui n'envoie jamais rien ne retienne pas de ressources.

## Mise à jour

```bash
ajean update
```

Rien à migrer : presets, configuration et mémoire sont inchangés.
