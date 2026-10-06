AJEAN MoE 1.0 : le moteur optimisé pour Qwen3.8 Flash Next, installé en un clic.

## AJEAN MoE

- **Installation en un clic.** La section Moteur propose « Qwen3.8 Flash Next » sur les machines compatibles (Linux x86_64, carte NVIDIA RTX 20 ou plus récente avec au moins 8 Go de VRAM, Python 3.10 ou plus récent). Le choix porte sur la version (Swift 1.5 ou Classique) et la qualité ; le moteur, le modèle et tous les réglages sont installés puis activés automatiquement. Les paquets sont figés et vérifiés par SHA-256.
- **Réglages adaptés à la machine.** Une deuxième carte NVIDIA sert de carte d'appoint : elle garde une part des experts dans sa VRAM et les calcule elle-même. La RAM disponible détermine le mode : experts entièrement en RAM, experts verrouillés en RAM sauf ceux de la carte d'appoint, ou lecture depuis le disque (mmap) quand la RAM est insuffisante.
- **Experts de la carte d'appoint hors RAM.** Les experts gardés par la carte d'appoint ne sont plus copiés en RAM, ce qui permet de verrouiller le reste en mémoire sur une machine qui ne pouvait pas tout contenir. Mesuré sur une RTX 5060 Ti 16 Go + RTX 3070 8 Go avec 46 Go de RAM (Swift 1.5 IQ3_XXS) : génération de 56 à environ 64 tok/s, lecture d'un prompt de 19 000 jetons de 836 à environ 980 tok/s par rapport au mode mmap.
- **Fenêtre du modèle.** L'engrenage du preset ouvre ses propres réglages : contexte réglable de 32 768 à 262 144 jetons, cache KV, prédiction MTP, lecture des images, carte d'appoint. La configuration réellement lancée y est détaillée.
- **Configuration.** Le panneau affiche le moteur (AJEAN MoE), le modèle, le contexte, le cache KV, le MTP, la taille des blocs de lecture et le mode des experts, avec la commande de lancement copiable.

## Corrections

- **Benchmark du moteur MoE.** Un deuxième benchmark relisait le prompt depuis le cache du moteur et affichait une lecture de 5 jetons. Chaque benchmark relit désormais le prompt en entier, et son résultat apparaît sur le preset.
- **Accès distant et point d'accès OpenAI.** Certains moteurs refusaient (403) les requêtes relayées par ajean.link, à cause d'un nom d'hôte inconnu. Le relais présente désormais le nom local.
- **Interface.** Coins arrondis corrigés sur les listes sélectionnables (section Moteur, choix de la qualité), icône du benchmark visible dans la fenêtre du modèle.

## Mise à jour

    ajean update

Vérifié par les tests automatiques et sur le serveur Linux de test (RTX 5060 Ti + RTX 3070, 46 Go de RAM, Swift 1.5 IQ3_XXS). Non testé : une installation complète sur une machine vierge, une machine à une seule carte, les autres qualités (IQ2_XS, Q2_0, IQ3_S).
