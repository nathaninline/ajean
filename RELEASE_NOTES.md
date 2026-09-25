Cette version ajoute les presets GPU cloud : un modèle peut tourner sur une carte graphique louée à la seconde, sans rien installer sur la machine. Elle regroupe aussi les modes d'exécution dans l'éditeur de preset, corrige le décompte des lignes écrites par l'IA et range les sauvegardes ajean.link par machine.

## GPU cloud (Modal)

Un preset peut désormais s'exécuter sur un GPU loué chez Modal (modal.com) au lieu de la machine locale. Tous les réglages habituels restent disponibles (contexte, cache KV, décodage spéculatif MTP, flash attention, chargement du modèle, raisonnement) et sont appliqués au llama-server distant.

- **Aucune installation** : au premier usage, AJEAN télécharge seul un Python portable (somme SHA-256 vérifiée) et le client Modal, dans son propre dossier.
- **Déploiement automatique** : activer ou modifier un preset GPU cloud le déploie. Le modèle (lien Hugging Face direct vers le .gguf) est téléchargé une fois côté Modal.
- **Facturation à l'usage** : le GPU s'éteint après un délai d'inactivité réglable. Le premier message suivant le réveille, AJEAN attend la fin du chargement.
- **Choix du GPU** : du T4 au B200.
- **Plusieurs comptes** : chaque preset peut utiliser son propre compte Modal. Un compte se connecte depuis l'éditeur, par la page de connexion de modal.com, sans saisir de clé.
- **Crédit** : le crédit restant et la consommation du mois s'affichent dans l'éditeur et sur la carte du GPU.
- **Suivi** : la carte « Appareil » montre la VRAM utilisée, l'occupation et la température du GPU distant quand il est allumé, sans le réveiller. La section « Configuration » détaille ce qui est envoyé au GPU.
- L'endpoint est protégé par une clé propre à la machine.

## Éditeur de preset

- **Exécution** : un seul sélecteur en tête de l'éditeur, « Cette machine », « API externe » ou « GPU cloud ». L'API externe (compatible OpenAI) se configure maintenant au même endroit que le reste, avec un test de connexion. Le bouton séparé de la liste des presets disparaît.
- **Fournisseur** : le mode GPU cloud propose un choix de fournisseur, Modal pour l'instant.
- **Chargement du modèle** : les deux interrupteurs « Garder en RAM » et « Charger tout en mémoire » sont remplacés par un sélecteur des modes de llama.cpp (auto, mmap, none, mlock, mmap+mlock, dio). Les versions récentes de llama.cpp ont remplacé les anciens drapeaux par `--load-mode`. Pour un moteur plus ancien, AJEAN retraduit automatiquement au lancement, et les presets existants s'affichent sans modification.
- Sous-titres raccourcis, et listes déroulantes ajustées à l'option choisie : les libellés ne se replient plus sur plusieurs lignes, notamment sur mobile.
- Liste des presets : l'icône d'une API externe rejoint les autres icônes de capacités (vision, raisonnement), et un preset externe multimodal affiche l'œil.
- Le bouton de mesure des performances est masqué pour les presets distants, où il n'avait pas de sens.

## Corrections

- **Décompte des lignes écrites** : quand l'IA écrivait un fichier de 500 lignes, le compteur affichait « +500 » pendant l'écriture puis retombait à « +120 ». Le diff envoyé à l'interface est limité à 120 lignes et le total était recompté sur cette version tronquée. Le vrai total est maintenant calculé avant la coupe, dans l'interface comme dans `ajean chat`.
- **Modifications dans un gros bloc** : une retouche d'une ligne dans un bloc de plus de 400 lignes s'affichait comme un remplacement complet. Les lignes identiques en début et en fin sont désormais écartées : le diff montre le vrai changement avec trois lignes de contexte, et ce changement n'est plus repoussé hors des lignes affichées.
- Un fichier terminé par un saut de ligne ne compte plus une ligne de trop.
- **Accès distant et horloge** : sur une machine dont l'horloge dérive de plus de 90 secondes, toutes les requêtes du portail ajean.link étaient refusées (serveur bloqué sur « démarrage », réponses coupées en cours de route). AJEAN mesure maintenant l'écart avec le relais et le compense.
- Démarrer le moteur alors qu'un preset externe est actif ne relance plus un llama-server sans modèle en boucle : le moteur local est arrêté.

## Sauvegardes ajean.link

Les sauvegardes sont maintenant rangées par machine. Le relais gardait les dix dernières sauvegardes du compte toutes machines confondues : une nouvelle machine en sauvegarde quotidienne faisait disparaître en dix jours celles d'une machine arrêtée. Chaque machine conserve désormais ses propres versions.

La restauration propose de choisir la sauvegarde (machine d'origine et date), en présélectionnant celle de la machine courante. Auparavant, la plus récente du compte était restaurée, même si elle provenait d'une autre machine. Les sauvegardes antérieures restent disponibles sous « ancienne sauvegarde ».

## Mise à jour

    ajean update

Non testé : la version Linux et macOS de ces changements (vérifiés sous Windows), le parcours GPU cloud complet sur une machine vierge, l'ajout d'un second compte Modal, et le chargement du modèle avec `--load-mode` sur un moteur local.
