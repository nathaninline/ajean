Piloter un autre PC fonctionne maintenant à distance, à travers ajean.link, sans que le relais ne voie jamais rien.

## Le poste distant passe par ajean.link, chiffré de bout en bout

La 0.8.9 a introduit le poste distant, mais uniquement sur le réseau local. Il fonctionne désormais **depuis n'importe où**, via ajean.link — et avec la même exigence que le reste d'ajean.link : le **relais reste aveugle**.

Concrètement, l'identité d'un poste n'est plus une clé partagée mais une **paire de clés** qui lui est propre. À l'appairage, le poste scelle sa demande vers la clé publique de ton serveur : le relais ne voit ni le code, ni rien d'autre. Ensuite, tout ce qui circule entre le poste et ton serveur — les commandes de l'IA comme leurs résultats — est **chiffré de bout en bout** avec une clé que seuls ton serveur et ton poste peuvent calculer. Le relais ne transporte que de l'opaque.

## Rien à changer pour toi

Dans **Réglages → Postes distants**, générer un code affiche maintenant deux commandes : une pour l'**accès à distance** (via ajean.link) et une pour le **réseau local** (connexion directe). Tu choisis, tu copies, tu colles sur l'autre PC.

## Un client, toujours aussi léger

Le petit client s'appelle **ajean-remote** (quelques mégaoctets, aucune interface). Il s'installe en service — invisible, au démarrage, reconnexion automatique — et se télécharge depuis ajean.link, section « Piloter un autre PC ».
