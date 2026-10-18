# Moteurs AMD : Radiance, Paiton et Strata HIP

[English](amd-engines.md) · **Français**

AJEAN est l'application : chat, modes Fast/Project/Jean/Base, historique,
mémoire, outils et compaction. Un **moteur** exécute les poids du modèle.
**Qwen** et **Swift** désignent des modèles ou variantes, pas des moteurs. Une
recette associe une révision exacte du modèle, un format de poids, un binaire
moteur, une topologie matérielle et des réglages compatibles.

Cette PR ajoute un **import local Strata HIP**. Elle n'ajoute pas trois moteurs
AMD administrés. Radiance et Paiton peuvent utiliser le transport API externe
AJEAN existant si leur serveur configuré fournit une API de chat compatible.
Les trois noms de moteurs affichés dans un déploiement de développement ne
constituent pas une intégration des trois moteurs dans cette PR.

## Architecture et état de l'intégration

| Moteur | Logiciel qui exécute l'inférence | Intégration dans cette PR | Preuves et travail restant |
| --- | --- | --- | --- |
| **Radiance** | Moteur AMD autonome publié par StillDeadcode. | Transport des presets API externe déjà présent ; aucun installateur Radiance, configuration de lancement administrée ou éditeur de réglages propre au moteur ajouté. | Radiance est le moteur AMD actif d'un déploiement de développement séparé. Ce constat ne qualifie pas cette PR sur GPU ; son code et sa configuration de déploiement ne sont pas inclus ici. |
| **Paiton** | Pour la génération de texte décrite ici, les composants runtime/plugin Paiton fonctionnent avec un environnement vLLM compatible, qui sert l'API de chat. | Un serveur déjà lancé et compatible peut être joint par l'API externe existante. Cette PR n'ajoute ni ne qualifie un adaptateur ou un installateur Paiton administré. | Respecter les versions et poids exigés par la recette. Des mesures historiques réalisées ailleurs ne valident pas une nouvelle installation AJEAN. |
| **Strata HIP** | Backend AMD HIP explicite de Strata, avec son exécutable, sa configuration serveur, son tokenizer et ses assets compatibles. | Nouveaux import/installation de bundle local épinglé, prévol, préparation de presets inactifs, variantes de réglages déclarés et passage au processus par AJEAN. | Ces chemins d'intégration sont testés avec des fixtures CPU. Exécution GPU, qualité, performances et concurrence de la recette importée restent à qualifier. |

La [page du distributeur Radiance](https://hub.docker.com/r/stilldeadcode/radiance)
identifie le moteur autonome et renvoie vers ses
[sources](https://codeberg.org/StillDeadcode/radiance). Il ne faut pas en déduire
qu'il s'agit de Strata, ni qu'une ancienne image nommée `vllm-radiance` possède
le même contrat d'exécution. Aucune filiation de code n'est affirmée ici.

Le [projet Paiton](https://github.com/Eliovp-BV/paiton-vllm-plugin) décrit son
chemin de génération de texte, ses presets par modèle et leurs exigences vLLM.
Le [guide AMD de Strata](https://github.com/Niko1221/Strata/blob/main/docs/AMD_HIP.md)
décrit son backend HIP. Ces projets évoluent indépendamment : épingler les
versions utilisées ; une documentation courante n'est pas une preuve de
qualification de l'installation.

## Connexion API et gestion d'un moteur local

Les presets API externe AJEAN acceptent déjà `EXTERNAL=1`, `EXTERNAL_URL`,
`EXTERNAL_MODEL` et une clé `EXTERNAL_KEY` facultative. Ils réutilisent le chat
original ; voir le [transport existant](../internal/ajean/backend_external.go).
Une API HTTP compatible n'installe pas le moteur, ne convertit pas ses poids
et ne garantit pas tous les paramètres de génération, outils ou vision.

La nouvelle branche `MOE_BACKEND=hip` importe une installation locale
**Strata** dans le workflow MoE existant ; voir le
[guide d'import](import-amd-hip.fr.md) et
[l'implémentation](../internal/ajean/backend_moe_hip.go). Elle ne transforme pas
les assets Radiance ou Paiton en assets Strata. Le modal prépare des presets
inactifs ; leur activation explicite par les interfaces de service/presets
existantes peut démarrer le moteur et reste séparée de la validation CPU.

## Les réglages appartiennent à une recette vérifiée

- **Quantification des poids :** une recette Radiance IQ4R, une recette Paiton
  W3A4/MXFP4 et une recette Strata GGUF/packs utilisent des assets différents.
  Un nombre de bits proche ne rend pas leurs fichiers interchangeables.
- **Contexte :** respecter la fenêtre totale du moteur, entrée et sortie
  comprises. Une fenêtre de 131072 tokens ne permet pas environ 128K en entrée
  plus 4096 en sortie. Ce plafond concerne l'import Strata de cette PR, pas
  les presets API externe d'autres moteurs.
- **Cache KV, MTP/spéculation et réflexion :** formats, assets de brouillon ou
  tête de prédiction, noms d'arguments et efforts dépendent du binaire et du
  modèle exacts. N'exposer que les choix déclarés compatibles, sans convertir
  silencieusement une valeur refusée ni copier les réglages d'un autre moteur.
- **Plusieurs GPU et concurrence :** répartition par couches, parallélisme
  tensoriel et instances indépendantes sont des topologies différentes. Une
  demande mise en file ne prouve pas un décodage simultané.

## Étendre l'intégration aux trois moteurs

Un catalogue commun peut séparer identité du moteur, famille du modèle et
identité de la recette. Chaque adaptateur doit déclarer identité/santé vérifiées,
capacités, réglages acceptés et opérations d'installation ou de cycle de vie
réellement prises en charge. Les actions indisponibles doivent l'indiquer avec
une raison. Tous les adaptateurs doivent conserver le chat, les modes, la
mémoire et la compaction AJEAN d'origine.

C'est un travail d'intégration supplémentaire, pas du code déjà livré dans cette
PR. La qualification doit identifier binaire, assets, matériel AMD, longueurs
réelles d'entrée/sortie et concurrence. Tests CPU d'import, réponse de santé API
et benchmark historique sont des preuves distinctes à présenter comme telles.
