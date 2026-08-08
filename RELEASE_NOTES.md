Trois retours d'utilisateurs après la 0.8.3, trois vrais trous dans le logiciel. Ils sont bouchés.

## Récupérer sa conversation

Jusqu'à la 0.7, l'historique vivait dans un fichier `conversation.json` qu'on pouvait ouvrir, relire et copier pour archive. La 0.8 l'a rangé dans `ajean.db`, une base binaire, verrouillée tant que le service tourne : impossible ne serait ce que de la copier. Le fil et les raisonnements étaient toujours là, mais plus personne ne pouvait les sortir. C'était une régression, et elle n'aurait pas dû passer.

Deux façons de récupérer le fil, désormais.

Depuis l'interface, dans le panneau « Actions » : un bouton exporte la conversation en Markdown (les raisonnements sont repliés sous un « Raisonnement », les appels d'outils apparaissent avec leur résultat), un autre en JSON fidèle si vous voulez retraiter les données.

Depuis le terminal :

```bash
ajean export                  # ajean-conversation-<date>.md
ajean export --json           # même chose en JSON
ajean export mon-fil.md       # nom imposé, l'extension choisit le format
ajean export -                # sur la sortie standard, pour enchaîner un tube
```

La commande fonctionne pendant que le service tourne.

## Le moteur joignable depuis le réseau

Symptôme rapporté : « à part le chat dans le navigateur, impossible d'utiliser ton URL dans les logiciels en local ». L'adresse `http://<machine>:8080/v1` s'affichait bien dans l'interface, mais rien ne répondait depuis un autre ordinateur.

Deux causes, cumulées, et aucune des deux n'était visible.

Sous Windows, l'installation posait `HOST=127.0.0.1` : le moteur n'écoutait que sur la machine elle même. Le chat du navigateur marchait, puisqu'il passe par le serveur web d'AJEAN, sur place. Tout le reste était invisible. Et ce réglage n'était modifiable nulle part dans l'interface, il fallait connaître `ajean edit` et savoir quoi y écrire.

Deuxième cause : même ouvert sur toutes les interfaces, le pare feu Windows bloque les connexions entrantes tant qu'aucune règle n'autorise le port. AJEAN n'en posait aucune.

Il y a maintenant un interrupteur « joignable depuis le réseau local », dans le panneau « Accès OpenAI », juste sous l'adresse qu'il conditionne. Il règle l'adresse d'écoute et pose la règle de pare feu dans le même geste. Poser une règle exige les droits administrateur, que l'installation d'AJEAN ne réclame pas : quand ça échoue, l'interface le dit et donne la commande exacte à coller dans un terminal administrateur, au lieu de laisser croire que c'est fait.

En ligne de commande : `ajean network`, `ajean network on`, `ajean network off`. Le moteur doit redémarrer pour appliquer, l'interface le propose.

L'adresse d'écoute est aussi devenue un réglage de machine et non de modèle : basculer sur un preset écrit avant cette version ne la remet plus à zéro. Une machine volontairement fermée reste fermée.

## Les modèles en plusieurs fichiers

Au delà d'une certaine taille, un dépôt Hugging Face publie son GGUF en tranches : `...-00001-of-00003.gguf`, et ainsi de suite. llama.cpp n'a besoin que de la première, il ouvre les suivantes tout seul. AJEAN, lui, traitait chaque tranche comme un modèle indépendant, avec trois conséquences.

Le lien collé ne rapatriait qu'un fichier sur trois, et le moteur mourait ensuite sur un tenseur introuvable. Le sélecteur affichait trois entrées pour un seul modèle, dont deux qui ne démarrent pas. Et supprimer « le modèle » n'effaçait que sa première tranche, laissant des dizaines de Go que plus rien ne référençait et que l'interface ne savait plus montrer.

Une famille de tranches est maintenant un seul modèle. Coller le lien de n'importe laquelle télécharge la famille entière, avec une barre de progression unique et un compteur de fichiers ; la vérification d'espace disque porte sur le total, plus sur une tranche. La liste n'affiche que la première, avec la taille de l'ensemble et une mention « 3 fichiers ». S'il en manque une, elle est signalée dans la liste, et le démarrage du moteur s'arrête sur un message qui nomme le fichier absent au lieu de laisser le service boucler. La suppression emporte toutes les tranches.

Un téléchargement interrompu à la deuxième tranche peut être relancé : les fichiers déjà complets sont conservés.

## Mise à jour

```bash
ajean update
```

Puis, si vous utilisez l'accès distant, rechargez la page du portail pour prendre la nouvelle interface.

Ce qui n'a pas été vérifié sur machine réelle : le téléchargement d'un vrai modèle découpé de bout en bout (la logique est couverte par des tests, mais pas un transfert complet depuis Hugging Face), et le comportement sur macOS, toujours pas testé sur un Mac.
