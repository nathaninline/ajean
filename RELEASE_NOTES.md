Correctifs du chiffrement de la mémoire et de l'affichage des images, après la v0.17.7.

## Chiffrement de la mémoire

- **La désactivation du chiffrement va désormais au bout.** Une seule valeur illisible (chiffrée avec une ancienne clé) arrêtait toute l'opération avec « déchiffrement refusé », sans dire laquelle. Ce qui peut être déchiffré l'est ; ce qui ne le peut pas est mis de côté, toujours chiffré, dans un dossier `chiffre-illisible-<date>` de `AJEAN_HOME`, avec une copie du trousseau de clés, et le journal du serveur indique chaque valeur concernée.
- **Tous les fichiers de la mémoire sont déchiffrés**, sous-dossiers compris. Auparavant seules les pages `.md` l'étaient : les autres fichiers du mode Jean (règles, compteurs, copies de sécurité du ménage) restaient chiffrés sans plus aucune clé une fois le chiffrement retiré.
- **La reprise d'un déchiffrement interrompu** au démarrage déchiffre maintenant aussi les conversations avant de retirer la clé. Elle pouvait les laisser chiffrées et illisibles.

## Images

- Un lien vers une image dans une réponse affiche l'image dans le chat, au lieu d'un simple bouton de téléchargement. Un clic l'agrandit.

## Mise à jour

    ajean update

Vérifié sur le serveur Linux (désactivation du chiffrement sur une vraie mémoire contenant d'anciennes conversations illisibles, puis réactivation et nouvelle désactivation) et par les tests automatiques. L'affichage des images derrière app.ajean.link n'a pas encore été éprouvé en conditions réelles.
