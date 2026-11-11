Version corrective de la 0.20 : reprise d'une installation passée par la 0.19, et éditeur des presets Service.

## Corrections

- **Un modèle de la 0.18 lancé par sa propre unité système ne redémarrait plus après la mise à jour.** Son preset pointait encore vers l'unité que la 0.19 avait remplacée par un service ; le moteur s'arrêtait sur une erreur sudo. Au premier lancement, ce preset devient un preset Service avec la commande du service de la 0.19, et ses autres réglages sont gardés.
- **`sudo ajean install` laissait des restes de la 0.19.** Les unités `ajean-svc-…` étaient seulement arrêtées. Elles sont maintenant supprimées, comme le gardien de mémoire (`ajean-guard`) et l'assistant administrateur de la 0.19 (`/usr/local/sbin/ajean-svc` et sa règle sudoers), que la 0.20 n'utilise plus.
- **Les étapes affichées à la fin de `ajean install`** citaient des commandes retirées en 0.20 (`ajean llamacpp install`, `ajean edit`).

## Presets Service

- **La commande de lancement est affichée en entière** : le champ prend la hauteur de son texte, plus besoin de faire défiler.
- **Plus de section API dans un preset Service.** L'adresse est déduite du `--port` de la commande et la clé de son `--api-key`. Restent le modèle, la réflexion, le contexte, la vision et le test de connexion. Une adresse ou une clé déjà enregistrées sur un preset restent utilisées.

## Mise à jour

```
ajean update
```

Depuis la 0.19 sous Linux, relancer une fois `sudo ajean install` pour retirer les anciennes unités.

Testé par les tests automatiques et sur un serveur Linux passé par la 0.18 puis la 0.19 (reprise du preset, nettoyage par `ajean install`, modèle relancé). L'éditeur Service n'a pas été vérifié à l'écran.
