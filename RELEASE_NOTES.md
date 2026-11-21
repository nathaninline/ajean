La 0.21.1 termine ce que la 0.21.0 avait laissé en ligne de commande, corrige deux problèmes signalés (benchmark coupé, CUDA écarté sous Windows) et simplifie l'interface.

## Moteur et presets dans l'interface

- **llama.cpp stable.** Le moteur épinglé de la 0.21 (llama.cpp **b12257**, testé pour cette version d'AJEAN) a sa carte dans la section Moteur : installer, réparer, imposer une variante (CUDA, Vulkan…) quand la machine en propose plusieurs. La variante installée y reste affichée.
- **Choix du moteur d'un preset en liste.** « Moteur utilisé » propose Stable, Précompilé, Compilé, Personnalisé et, sur carte NVIDIA, ExLlama, avec une ligne d'explication. Un moteur absent affiche toujours l'avertissement et le bouton d'installation.
- **Presets Service depuis l'éditeur.** Exécution > Service : une commande (vLLM, un autre llama-server, TabbyAPI…) lancée par AJEAN, dont l'adresse et la clé se déduisent de `--port` et `--api-key`. Les presets Service ont leur étiquette dans la liste.
- **API externe : l'échantillonnage qui a du sens.** Température, top_p et pénalité de présence restent réglables et sont envoyés ; top_k, min_p et la pénalité de répétition, propres à llama.cpp, ne partent plus vers une API distante (OpenAI les refusait). Les presets locaux et Service gardent tout.
- **Liste des modèles d'une API** : elle se charge aussi quand le coffre des clés est activé.

## Corrections signalées

- **Benchmark coupé sur les grosses mesures** (#203). L'interface abandonnait au bout de 30 secondes, le serveur au bout de 5 ou 10 minutes : une mesure de 100k jetons lus ou 3k générés dispose maintenant de 30 minutes.
- **Windows : CUDA écarté sur un refus passager, repli silencieux sur Vulkan** (#199). Quand l'antivirus analyse encore les fichiers tout juste extraits, la mise en place du moteur est réessayée quelques secondes. Si elle échoue quand même, l'installation s'arrête avec le message au lieu de passer à une variante moins bonne.

## Interface

- **Le mode Jean ressemble aux autres modes** : le personnage, la scène plein écran et la conversation à voix haute sont retirés. La dictée vocale du champ de saisie reste.
- **Accès internet et contrôle du navigateur** passent dans Paramètres > Général, activés par défaut sur une installation neuve. Une installation existante garde son réglage.
- **Interrupteur « applications »** (Paramètres > Général) : coupé, l'écran Applications disparaît et l'IA ne crée plus d'apps ; celles qui existent restent sur le disque.
- **Barre latérale ajustable** : glisser son bord droit, double-clic pour revenir à la largeur d'origine.
- **Bulles d'aide « ? »** redessinées, placées sans sortir de l'écran.
- **iPhone** : choisir dans une liste ne rouvre plus le sélecteur d'iOS.
- Détails : onglets de la mémoire de Jean qui grossissaient au survol, coins et séparateurs doublés dans les cartes de réglages.

## Sécurité et sauvegarde (suite de la 0.21.0)

- La sauvegarde locale emporte les images et vidéos des conversations, et ne fige plus l'interface pendant qu'elle écrit une grosse base.
- Coffre des clés : une entrée abîmée n'empêche plus la sauvegarde ajean.link ; après `sudo ajean secrets on`, la clé reste lisible par le service.
- Validation des tâches : un script apparu après la validation est retiré au refus, au lieu de revenir sans fin.

## Après la mise à jour

- **Depuis la 0.21.0, la 0.20 ou la 0.18.17 : rien à faire.** Presets, moteurs, clés et réglages sont repris tels quels.
- La 1.0 n'est plus développée : AJEAN continue sur cette lignée.

## Mise à jour

```
ajean update
```

Testé par les tests automatiques sous Windows et à l'écran (moteur, éditeur de preset Local / API / Service, réglages, mode Jean, barre latérale). Le correctif iPhone n'a été vérifié que dans un navigateur de bureau.
