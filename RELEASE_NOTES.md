Formules LaTeX dans les réponses, changement de modèle fiabilisé sous Windows, et plusieurs corrections remontées par les utilisateurs.

## Nouveautés

- **Formules LaTeX.** Les formules écrites par le modèle (`$…$`, `$$…$$`, `\(…\)`, `\[…\]`) s'affichent désormais mises en forme au lieu du code brut. Le moteur de rendu (KaTeX) est embarqué dans l'application et ne se charge qu'à la première formule. Le code et les montants comme « 5 $ » ne sont pas touchés. (#123)
- **Délais MCP réglables.** Le panneau des serveurs MCP propose un délai de connexion (20 s par défaut) et un délai d'appel d'outil (120 s par défaut), de 1 à 3600 s. Un serveur distant lent ou un outil long n'échouent plus faute de temps. Contribution de Trombo38. (#122)
- **Branche d'un fork llama.cpp.** L'installation d'un backend personnalisé accepte une branche, un tag ou un commit, et l'adresse GitHub d'une branche (`…/tree/<branche>`) peut être collée telle quelle. Auparavant, seule la branche par défaut pouvait être compilée. (#116)

## Corrections

- **Changement de modèle sous Windows.** Deux changements rapprochés pouvaient lancer deux moteurs à la fois : la RAM se remplissait, le nouveau moteur échouait sur « port 8080 déjà utilisé », et seul un redémarrage du PC libérait la mémoire. Les redémarrages du moteur passent maintenant un par un, l'ancien moteur est attendu jusqu'à sa fin réelle, et llama-server ne peut plus survivre au service qui l'a lancé. (#114)
- **Menus refermés pendant la génération.** Le défilement automatique du chat refermait les menus ouverts (projets, sélecteurs des réglages). Un menu ne se ferme plus que si le défilement le déplace. (#113)
- **CUDA non détecté sous Arch.** Le toolkit installé dans `/opt/cuda` passait inaperçu et la compilation retombait sur Vulkan. `/opt/cuda`, `CUDA_PATH` et `CUDA_HOME` sont désormais pris en compte. (#117)
- **Moteur Qwen3.8 Flash Next invisible.** Sur Ubuntu et Mint, le module venv de Python n'est pas installé par défaut : la ligne du moteur disparaissait de la section Moteur sans explication. Elle reste visible et indique le prérequis manquant (`sudo apt install python3-venv`). (#125)
- **Accès à l'API plus léger.** L'empreinte de la clé de pilotage n'est plus relue dans la base à chaque requête de l'interface. Une lecture ratée continue de fermer l'API. (#120)

## Mise à jour

    ajean update

Vérifié par les tests automatiques, et le rendu des formules dans le navigateur. Non testé en conditions réelles : le changement de modèle sous Windows, la détection de CUDA sous Arch, la compilation d'une branche de fork et l'affichage du moteur MoE sur Ubuntu sans venv.
