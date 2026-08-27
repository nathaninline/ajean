Corrections autour de l'historique et des exports, et finitions de l'affichage de l'activité.

## Historique des conversations

* Correction de conversations qui apparaissaient tronquées en rouvrant une session (premiers messages manquants). Deux causes : un curseur de replay hérité d'une autre session pouvait faire sauter le début du fil rejoué ; et le journal d'affichage enflait (les appels d'outils n'étaient pas coalescés) jusqu'à ce que ses plus anciens événements soient tronqués. Les appels d'outils sont désormais coalescés en fin de tour, et le rejeu repart du début quand le curseur ne correspond pas à la session ouverte.

## Exports

* Export JSON : les images des conversations avec vision ne sont plus incluses en base64. Elles pesaient plusieurs méga-octets chacune et gonflaient le fichier ; elles sont maintenant remplacées par un court marqueur indiquant leur taille. L'export Markdown n'était pas concerné.
* Export Markdown : chaque appel d'outil n'apparaît plus qu'une seule fois (il pouvait être écrit en double lorsqu'un autre événement s'intercalait entre l'appel et son résultat).

## Affichage de l'activité

* Bulle d'appel d'outil épurée : plus de cadre ni d'en-tête redondant, la commande et le résultat s'affichent en blocs de code sobres.
* Libellés d'outils clarifiés (Terminal, Recherche web, Lecture mémoire, Nouvelle tâche, etc.).
* Correction du compteur de modifications qui pouvait s'empiler, et marqueur de diff « + / - » désormais séparé du texte pour rester lisible sur une ligne commençant par un tiret.
* Réglages : la section d'accès distant est regroupée sous « ajean link » (accès distant et sauvegarde).

## Mettre à jour

```
ajean update
```
