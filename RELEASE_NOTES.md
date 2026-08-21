Grosse version : l'historique des conversations arrive, le pilotage d'un PC distant sait enfin te livrer des fichiers et agir dans ta session, et les cartes AMD sont enfin visibles. Plus une série de corrections d'affichage.

## Historique des conversations

* « clear chat » n'efface plus la conversation pour de bon : elle est rangée dans un historique. Un nouveau bouton « historique » (section Actions) ouvre la liste des conversations passées. Pour chacune, on peut la recharger (elle redevient la conversation active, celle en cours étant archivée à son tour, donc rien n'est perdu) ou la supprimer définitivement (issue #33).

## Interface

* Les fichiers joints à un message s'affichaient sur une seule ligne : au-delà de trois, les suivants étaient cachés, impossible de vérifier sa sélection avant d'envoyer. Ils s'enroulent maintenant sur plusieurs lignes (issue #36).
* Une erreur rouge du moteur « llama personnalisé » restait affichée pour toujours, même après un redémarrage : elle était rechargée à chaque démarrage. Un bouton « masquer » la fait disparaître définitivement (issue #37).
* La petite ligne d'état sous la réponse (chrono, tokens, vitesse) disparaissait quand on actualisait la page pendant que l'IA répondait. Elle reste maintenant affichée, avec un chrono qui reprend à la bonne valeur (plus de retour à zéro) et une vitesse en tokens par seconde correcte tout de suite.
* Arrêter une génération affichait un message technique disgracieux (« context canceled ») : c'est désormais une note discrète « génération interrompue ».
* Recharger une conversation depuis l'historique ouvrait puis refermait d'un coup les bulles de raisonnement et d'appel d'outil : elles arrivent maintenant directement repliées, comme au chargement d'une page.

## Cartes graphiques AMD

* La carte « GPU / VRAM » de l'interface n'interrogeait que NVIDIA : une carte AMD n'apparaissait pas du tout. Elle est maintenant détectée via amd-smi (nom, VRAM, charge, température), en repli quand aucune carte NVIDIA n'est trouvée (issue #32).

## Pilotage d'un PC distant

* L'IA ne pouvait pas te livrer un fichier créé sur le PC distant : la capacité n'existait tout simplement pas. Elle peut désormais rapatrier un fichier du poste, et les liens de téléchargement fonctionnent comme en local.
* Le poste tournant en service système, l'IA restait coincée sur le profil SYSTEM : elle ne voyait ni ton bureau, ni tes fichiers, ni tes applis. Quand tu es connecté, elle agit maintenant sous ton compte (ton bureau, tes fichiers, ton environnement) ; personne de connecté, elle retombe sur le service système comme avant.

## Installation Linux

* Détection Vulkan élargie (Fedora, RHEL et dérivés), et sur un système SELinux le binaire installé reçoit le bon contexte (restorecon) pour que le service démarre sans intervention manuelle (issue #28).
* Nouvelle option `ajean llamacpp install --backend cuda|hip|vulkan|cpu` pour forcer un moteur quand la détection automatique en choisit un cassé.

## Mettre à jour

```
ajean update
```

Testé sur le serveur de Alice. Le pilotage d'un PC distant (téléchargement de fichiers et exécution sous ta session) et la détection AMD n'ont pas encore été validés sur du vrai matériel : à confirmer, corrections dans une prochaine version si besoin.
