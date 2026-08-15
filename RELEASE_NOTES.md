Cette version corrige une erreur avec les modèles Qwen3.x en mode agent.

## Plus d'erreur « System message must be at the beginning »

Avec certains modèles (Qwen3.x notamment) lancés en mode strict, une commande pouvait échouer avec une erreur 500 de llama-server : « System message must be at the beginning ». En cause : dans un tour d'agent, après une commande d'outil ratée, AJEAN glissait une instruction système en fin de séquence pour relancer le modèle. Or ces gabarits exigent que le message système soit uniquement en tête, et refusaient la requête.

Désormais, juste avant chaque envoi, les messages système sont fusionnés en un seul, placé au début. La séquence envoyée reste donc toujours valide, quel que soit le gabarit du modèle. Pour une conversation normale, rien ne change : la séquence est identique à avant.

## Mise à jour

```
ajean update
```

Merci à juanpa669 pour le signalement clair et bien analysé.
