Correction d'un défaut d'indexation de la mémoire introduit en 0.13.0.

## Correction

* À la migration vers les projets, les pages de mémoire existantes étaient déplacées dans le projet « Générale » mais leur index (MEMORY.md) restait vide : l'assistant ne voyait alors plus la liste de ses pages. L'index est désormais reconstruit à partir des pages réellement présentes, à la migration et au démarrage d'une nouvelle conversation. Les projets déjà migrés se corrigent d'eux-mêmes dès la conversation suivante.
