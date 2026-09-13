Suite de la 0.13.12 : le mode agent désactivé devient un vrai chat brut, et le sélecteur de mémoire est repensé.

## Mode agent désactivé = modèle brut

Quand le mode agent est coupé, AJEAN n'ajoute plus rien à la conversation : ni préambule, ni prompt système de preset, ni contexte injecté (description du projet, index mémoire, trackers), ni outils. Le modèle reçoit uniquement les messages, comme si on parlait directement à un serveur llama.cpp nu.

Avant, le prompt système du preset restait injecté même en mode agent désactivé. Il décrivait des outils et une mémoire que ce mode ne fournit pas, ce qui poussait le modèle à écrire des appels d'outils en clair (blocs de type tool_call) dans ses réponses, sans effet. C'est réglé.

Note : cela vaut pour une conversation neuve. Une conversation déjà entamée en mode agent conserve le contexte déjà présent dans son historique.

## Sélecteur de mémoire

Le choix du mode mémoire (dans la fenêtre Mémoire) passe d'un rail horizontal, qui tassait les libellés, à une liste d'options lisibles : une icône, un titre et une courte explication par mode (Injectée, Recherche, Sur demande, Désactivée), avec l'option active mise en avant dans la couleur du thème.

## Mise à jour

```
ajean update
```

Non vérifié sur cette version : le comportement en mode agent désactivé a été validé sur conversation neuve, pas sur le basculement en cours de conversation longue.
