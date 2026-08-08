Une version de corrections, née de vos retours sur la 0.8.0 : un moteur qui refusait de démarrer sans jamais le dire, et des presets qui se mélangeaient.

## « ajean start » ne ment plus

Le cas le plus pénible remonté cette semaine : `ajean start` répondait `[ok] ajean-engine: activating`, puis `ajean test` répondait « /health ne répond pas ». Rien dans les deux messages ne permettait de comprendre.

L'explication tenait à une nuance de systemd. Un service qui meurt au lancement est relancé toutes les trois secondes, et pendant ces trois secondes il est en `activating`, exactement comme un moteur en train de charger un modèle. AJEAN prenait donc un échec en boucle pour un démarrage en cours. Il distingue désormais les deux : un vrai chargement reste annoncé comme tel (un gros .gguf prend des minutes, c'est normal), une boucle d'échec affiche l'erreur, les vingt dernières lignes du journal et quoi corriger.

Mieux : `ajean start` et `ajean restart` vérifient d'abord ce sans quoi le moteur ne peut pas démarrer, et refusent de lancer un service condamné. Moteur absent, modèle non renseigné, fichier .gguf introuvable : c'est dit tout de suite, avec la commande qui répare.

## Un modèle peut vivre où vous voulez

`MODEL=/home/moi/modeles/mon-modele.gguf` était refusé si le dossier n'avait pas été déclaré au préalable dans l'interface web. Le moteur mourait alors en boucle sur un « dossier non autorisé » que personne ne voyait passer, et le seul remède connu consistait à ouvrir l'interface pour y ajouter le dossier. Un utilisateur l'a trouvé tout seul, à tâtons ; ce n'était pas une découverte à lui imposer.

Ce garde-fou existe pour empêcher l'interface web de supprimer un fichier n'importe où sur la machine. Il n'avait aucune raison de s'appliquer à « ouvrir ce modèle en lecture ». Un chemin absolu vers un .gguf qui existe est maintenant accepté tel quel, quel que soit le disque. La protection reste entière là où elle sert.

## La configuration s'explique enfin

Depuis que la configuration vit en base, `ajean edit` déroulait dans votre éditeur les seules clés déjà définies. Sur une installation neuve, cela donnait un fichier quasiment vide : impossible de deviner quoi écrire.

L'éditeur propose désormais un squelette commenté. Chaque réglage utile y figure avec son rôle et son défaut (BIN, MODEL, CTX, NGL, batch, threads, cache KV, raisonnement, mémoire, EXTRA_ARGS). Les valeurs que vous avez déjà réglées sont en clair, les autres restent commentées, donc inactives, et toute clé que le squelette ne connaît pas est conservée en fin de fichier.

Les étapes affichées à la fin de `ajean install` ont été corrigées dans la foulée : elles oubliaient `ajean llamacpp install`, sans quoi la clé BIN reste vide et le moteur ne peut pas démarrer.

## Presets : un nouveau preset repart propre (issue #17)

Créer un preset avec le bouton « + » recopiait la configuration active en entier. Les réglages du modèle précédent (les experts déportés sur le CPU, la répartition entre cartes, le contexte, jusqu'au modèle lui-même) se mélangeaient donc aux options cochées pour le nouveau, et il fallait penser à tout nettoyer à la main.

Un nouveau preset ne reprend plus que ce qui relève de la machine : le moteur, l'adresse et le port. Tout le reste part des valeurs par défaut, que vous ajustez pour ce modèle-là.

## Presets : les guillemets ne sont plus mangés

Le rapport parlait de contenu tronqué et de guillemets non appariés, et c'était exact, avec une cause plus profonde qu'il n'y paraissait. En relisant une ligne de configuration, AJEAN retirait tout guillemet en début et en fin de valeur, sans vérifier qu'il s'agissait d'une paire. Une valeur comme :

```
EXTRA_ARGS=--jinja --chat-template-file "/etc/ajean/gabarit.jinja"
```

ressortait donc amputée de son guillemet final, déséquilibrée, et repartait ainsi dans le preset enregistré. Le défaut existait des deux côtés, dans le moteur comme dans l'interface. Seule une paire entourante est maintenant retirée, et l'écriture est faite pour être relue à l'identique. Un test verrouille l'aller-retour.

Dans la même zone : EXTRA_ARGS était découpé sur les espaces sans tenir compte des guillemets, si bien qu'un chemin contenant une espace devenait deux arguments et que llama-server refusait de démarrer. Le découpage respecte désormais les guillemets, comme le ferait un shell.

## Mise à jour

```bash
ajean update
```

Ou, depuis l'interface, le bandeau de mise à jour. Rien à migrer : les presets, la configuration et la mémoire sont inchangés.
