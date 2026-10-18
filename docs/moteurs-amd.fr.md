# Moteurs AMD : Radiance, Paiton et Strata HIP

[English](amd-engines.md) · **Français**

AJEAN fournit le chat, les modes Fast/Project/Jean/Base, l’historique, la mémoire, les outils et le compacteur. Un moteur exécute les poids. Qwen et Swift désignent des modèles ou variantes, pas des moteurs. Une recette associe révision exacte, format, build du moteur, topologie matérielle et réglages compatibles.

Cette PR livre deux parcours concrets dans l’application originale : **connexions nommées Radiance/Paiton** et **imports locaux Strata HIP épinglés**. Leur gestion du cycle de vie est différente.

| Moteur | Runtime d’inférence | Intégration livrée | Limites de gestion et de qualification |
|---|---|---|---|
| Radiance | Moteur autonome publié par StillDeadcode. | L’entrée moteur ouvre l’éditeur de preset externe original ; connexions enregistrées, découverte des modèles sans complétion, sélection explicite vérifiée pour le chat original. | AJEAN n’installe/recharge pas ce serveur et ne modifie pas ses GPU/KV/MTP/poids. Un ID annoncé ne prouve pas les poids chargés. |
| Paiton | Pour le texte, composants runtime/plugin Paiton avec un environnement vLLM compatible. | Même parcours nommé : URL/modèle/clé persistés, découverte et sélection explicite. | Déploiement, template, parseur d’outils et compatibilité runtime dépendent du serveur exploité séparément. |
| Strata HIP | Build AMD HIP explicite avec exécutable, configuration serveur, tokenizer et assets compatibles. | Import/installation de bundle local épinglé, prévol, presets inactifs, variantes de réglages déclarées et lancement AJEAN original. | Tests d’import avec fixtures CPU ; chaque recette réelle doit encore être qualifiée sur GPU en exécution, qualité et performances. |

Suivre les [connexions Radiance/Paiton](amd-engine-connections.fr.md) pour brancher un serveur, ou l’[import Strata HIP](import-amd-hip.fr.md) pour installer le moteur local. La détection matérielle automatique et le parcours CUDA restent inchangés même avec un catalogue HIP présent ; le mode d’import local est explicite.

## Les réglages appartiennent au serveur ou à la recette

Les connexions utilisent `EXTERNAL=1`, les champs URL/modèle/clé existants et `EXTERNAL_ENGINE=radiance|paiton`. AJEAN vérifie `GET /v1/models` ; l’enregistrement n’active rien. La sélection explicite refuse un modèle absent ou une génération/installation en cours, puis applique la configuration de chat habituelle sans action de service. AJEAN ne choisit pas silencieusement un modèle de repli. Le sens donné aux noms par le serveur reste à vérifier.

Contexte et vision sont des déclarations opérateur, pas des commandes de reconfiguration serveur. Les efforts Radiance proviennent des capacités déclarées du modèle ; Défaut du serveur omet la surcharge. La requête utilise `reasoning_effort` au premier niveau ; Paiton conserve son mapping existant. Quantification, précision KV, MTP/brouillon, placement GPU et concurrence native restent définis par la recette. Les `.rad` Radiance, checkpoints/artifacts Paiton et GGUF/packs Strata ne sont pas interchangeables. Un client en file ne prouve pas un décodage natif simultané.

Le modal et le chat restent ceux d’AJEAN. Les connexions externes n’introduisent pas un autre serveur de chat, proxy ou installateur moteur. Le moteur Strata importé démarre seulement par les actions explicites existantes de preset/service ; préparation et activation restent distinctes.

## Sources et preuves

L’[éditeur Radiance](https://hub.docker.com/r/stilldeadcode/radiance) renvoie au [code autonome](https://codeberg.org/StillDeadcode/radiance). Les anciennes images vllm-radiance suivent un autre contrat ; aucune filiation avec Strata n’est déduite. [Paiton](https://github.com/Eliovp-BV/paiton-vllm-plugin) documente ses runtimes et lanceurs par modèle. Le [guide AMD Strata](https://github.com/Niko1221/Strata/blob/main/docs/AMD_HIP.md) décrit son backend HIP. Figer les builds et assets réels.

Les [mesures historiques Flash Next](benchmarks/README.fr.md) donnent la matrice de six profils, les conditions et les empreintes des preuves brutes sans données privées. Quantification, cache, spéculation et répétitions diffèrent ; ces mesures de serveurs séparés ne qualifient ni le code de connexion de cette PR ni la vitesse isolée d’un moteur. Fixtures CPU, découverte API, bon fonctionnement des outils et débits GPU constituent des preuves distinctes.
