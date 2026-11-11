Retour à une identité sobre, l'IA a toujours accès à ses outils, et un grand ménage dans le code.

## Identité

- **L'orbe de lumière est retirée.** Plus d'animation qui suit la conversation : l'interface retrouve son calme.
- **Le J revient comme symbole** : icône d'onglet, icône du programme (`.exe`, zone de notification Windows, barre de menus macOS), écran de démarrage Windows, logo qui pulse au chargement et début des lignes d'activité.
- **Le logo est le mot AJEAN en pixels**, sans ajout.

## Mode agent supprimé

- **L'IA a toujours ses outils** : terminal, fichiers, mémoire, et selon les réglages accès internet, navigateur et serveurs MCP. Il n'y a plus de mode agent à activer ni de confirmation à donner.
- La commande `ajean agent` n'a plus d'options `on` / `off` : elle affiche les outils disponibles.

## Terminal

- **`ajean chat` adopte la nouvelle présentation** : le logo AJEAN en pixels à l'ouverture, une mise en forme monochrome (accents en gras, code en gris), et le rouge réservé aux erreurs.

## Nettoyage

- Retrait du code devenu inutile depuis la 0.19 : anciennes routes du serveur, lecture seule d'une autre conversation, vues d'historique des modes retirés, réglages grisés quand l'agent était coupé, styles et textes qui ne servaient plus. Environ 1 500 lignes en moins, sans changement visible.
- Les anciennes conversations Rapide et Modèle de base restent lisibles et toujours trouvables par la recherche de l'historique.

## Mise à jour

```
ajean update
```

Testé par les tests automatiques, sur un serveur Linux et dans une instance de test (chargement de l'interface, réglages, tâches et historique). La bannière de `ajean chat` n'a pas été vue dans un vrai terminal, et l'écran de démarrage Windows comme la barre de menus macOS n'ont pas été vus à l'écran.
