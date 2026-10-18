# Mesures historiques des profils Flash Next

Ces mesures réelles portent sur deux Radeon AI PRO R9700. Les poids, quantifications, KV, spéculation, caches et répétitions diffèrent : elles comparent des **profils modèle/moteur**, pas la vitesse isolée d’un moteur. Elles ne qualifient pas le code de connexion de cette PR sur GPU.

Débit agrégé = tokens de sortie / durée murale du groupe, **préfill et attente inclus**, sur une charge de code source ~30K. C représente les clients soumis. Strata ne dispose que d’une séquence native active : C2/4/8 attendent en file. Le préfill et le décodage natifs isolés ne sont pas disponibles de façon fiable pour ces cellules : N/D.

| Modèle réel | Moteur/profil | C1 | C2 | C4 | C8 |
|---|---|---:|---:|---:|---:|
| Qwen3.8 Flash Next | Paiton/vLLM | 77.47 | 136.62 | 137.65 | 164.85 |
| Qwen3.8 Flash Next | Radiance | 84.77 | 281.92 | 454.35 | 818.49 |
| Qwen3.8 Flash Next | Strata AMD | 34.83 | 61.43 | 59.27 | 56.14 |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Paiton/vLLM | N/D | N/D | N/D | N/D |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Radiance | 87.33 | 319.23 | 581.94 | 1025.36 |
| Swift1.5 Qwen3.8 Flash Next GSQ RCO | Strata AMD | 44.21 | 74.46 | 76.54 | 76.88 |

Valeurs en tok/s ; aucune n’est un décodage individuel par utilisateur.

- Radiance, 17 octobre : IQ4R experts/INT8 tronc, KV FP8, MTP 3, contexte 262144, budget sortie 4096, médiane de deux répétitions avec moteur rechargé, réutilisation naturelle des préfixes.
- Strata, 16 octobre : QwenUD-IQ4_XS et SwiftIQ3_XXS, KV FP16/résident32768, MTP 3, contexte 131072, budget sortie 4096, une grille retenue (Qwen avec cellules conservées/importées).
- Paiton Qwen, 12 octobre : FNQ / W3A4, TP2, KVauto dans la configuration native, MTP 0, contexte 98304, cache de préfixe désactivé ; entrée 30622 tokens, sorties 3397–4096 tokens. C1 est la médiane des groupes first/repeat/different ; les autres C ont un groupe chacun. Aucun raisonnement émis observé ; ce n’est pas une mesure low appariée.
- Paiton Swift : aucune mesure qualifiée du même Swift Flash Next trouvée. Swift 27B est un autre modèle et ne remplit pas cette cellule.

La campagne Radiance de reproductibilité ultérieure à ~128K/low/MTP 3 suit un autre protocole. Les recettes Strata 131072 refusent cette entrée avec 4096 tokens réservés (127685+4096 dépasse131072) ; le profil Paiton98K ne la représente pas non plus. N/D signifie absence de preuve comparable, pas débit nul.

Le [JSON](amd-engine-history.json) fournit les SHA256 des preuves brutes et les conditions exactes ; le [CSV](amd-engine-history.csv) contient les six lignes. Aucun prompt, réponse générée, chemin privé, adresse de serveur ou secret n’est inclus. Aucune nouvelle inférence ou sonde GPU n’a été lancée.
