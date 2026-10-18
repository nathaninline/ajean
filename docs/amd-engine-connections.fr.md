# Connexions Radiance et Paiton

AJEAN permet d’enregistrer, découvrir et sélectionner des connexions nommées Radiance ou Paiton dans l’interface originale des moteurs/modèles. Ce sont des presets API externe : le chat, ses quatre modes, les outils, l’historique, le chiffrement et le compacteur suivent le pipeline AJEAN existant. Le serveur du moteur est exploité séparément. L’import et l’installateur local Strata HIP constituent un autre backend.

## Brancher un serveur existant

1. Configurer et démarrer le moteur suivant son déploiement officiel. Relever son URL, son identifiant de modèle annoncé, sa clé éventuelle et son contexte réel.
2. Ouvrir l’entrée Radiance ou Paiton, puis créer une connexion dans l’éditeur de preset externe original : URL, modèle, clé si nécessaire, contexte et capacité vision déclarés.
3. Tester/découvrir les modèles. Pour ces moteurs nommés, AJEAN envoie seulement `GET /v1/models`, y compris avec une base terminée par `/v1`, et la clé Bearer éventuelle. Aucun appel de complétion n’est envoyé. Laisser le modèle vide pour découvrir la liste, puis choisir un identifiant annoncé exact.
4. Enregistrer : cela crée un preset inactif, sans sélectionner ni piloter le serveur.
5. Sélectionner explicitement la connexion enregistrée. AJEAN revérifie le modèle annoncé, refuse un identifiant inconnu ou une génération/installation en cours, puis applique sa configuration habituelle de chat. Aucun service n’est démarré, arrêté ou rechargé. La bascule de preset habituelle et la commande CLI de bascule suivent le même parcours pour une connexion nommée.

La découverte ne qualifie ni le matériel ni la qualité. `/models` est une déclaration du serveur, pas une preuve des poids, du contexte disponible ou du bon fonctionnement des outils. Radiance précise qu’une requête peut nommer n’importe quel modèle tout en étant servie par le modèle chargé : vérifier séparément la recette réellement exploitée.

## Configuration réutilisable

Le format de preset existant reçoit `EXTERNAL_ENGINE=radiance` ou `paiton` :

```dotenv
EXTERNAL=1
EXTERNAL_ENGINE=radiance
EXTERNAL_URL=http://127.0.0.1:8000/v1
EXTERNAL_MODEL=remplacer-par-un-identifiant-annonce-exact
CTX=131072
```

Remplacer le contexte d’exemple par celui du serveur. Le guide Paiton Qwen3.8 **27B** utilise `http://127.0.0.1:18982/v1`, modèle `Qwen3.8` ; Flash Next peut exposer une autre URL et un autre identifiant. Le nom du moteur ne les détermine pas. `EXTERNAL_KEY` conserve la clé Bearer éventuelle dans le preset local ; l’éditeur nommé écrit atomiquement avec des permissions réservées au propriétaire et ne renvoie pas la clé enregistrée en clair. Une édition sans toucher au champ clé la conserve. `EXTERNAL_VISION=1` est une déclaration opérateur, pas un contrôle automatique.

API : `GET /api/engine/connections` ; `POST /api/preset/external/test` et `/save` existants acceptent `engine`, `name`, `url`, `model`, `key`, `ctx`, `vision`, et éventuellement `effort`, `id`/`keyTouched` ; `POST /api/engine/connections/select` reçoit `{"id":"identifiant-du-preset"}`. Le contexte de cette requête est une chaîne. La liste expose `hasKey`, jamais la clé.

## Effort de raisonnement déclaré

L’éditeur propose **Défaut du serveur**. Pour Radiance, les autres choix proviennent uniquement de `capabilities.reasoning_efforts` du modèle sélectionné dans la découverte. Le test et la sélection refusent un effort non annoncé, sans alias silencieux. Un choix non vide est conservé dans `REASONING_EFFORT` et envoyé comme `reasoning_effort` au premier niveau, sans `chat_template_kwargs`. Vide signifie ne pas imposer de valeur ; omettre le champ API facultatif `effort` lors d’une édition conserve la valeur enregistrée.

La sélection conserve un instantané des capacités pour le raccourci du compositeur original. Retester/resélectionner après un changement de recette serveur ; ce contrôle ne reconfigure pas le serveur. La compaction Radiance demande `none` seulement si cet instantané l’annonce, sinon elle laisse le défaut serveur. Paiton et les connexions génériques/locales conservent leur comportement `chat_template_kwargs` existant. Ce mapping ne qualifie ni le raisonnement ni les outils.

## Conditions et limites

Le serveur doit fournir `POST /v1/chat/completions` compatible OpenAI, ses réponses en streaming et les appels d’outils utilisés par le mode AJEAN choisi. Radiance documente chat, outils, streaming et découverte dans son [guide officiel](https://codeberg.org/StillDeadcode/radiance/src/branch/main/docs/GUIDE.md). Les déploiements linguistiques Paiton utilisent l’API vLLM : suivre le [guide officiel du modèle](https://github.com/Eliovp-BV/paiton-vllm-plugin). Le parseur, le template, le raisonnement, les images et les outils dépendent de ce déploiement exact. Une découverte réussie ne les qualifie pas.

L’installation, les téléchargements, les mises à jour, les processus, le chargement/déchargement du modèle, le placement GPU, le contexte/KV/MTP/quantification et la concurrence côté serveur restent gérés séparément. Les capacités déclarées sont `install:false`, `reload:false`, `server_settings:false`. Les champs contexte/vision AJEAN ne reconfigurent pas le serveur. Aucun débit GPU, capacité multiutilisateur ou cycle de vie n’est déduit d’un nom de moteur ou de fixtures CPU. Figer et valider séparément le build/image et les poids.
