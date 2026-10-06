Mode Jean : preset dédié au sommeil, scène plus fiable et images de la bulle corrigées.

## Mode Jean

- **Preset du sommeil.** La fenêtre « Mémoire de Jean » propose une section Sommeil : le preset choisi sert à la consolidation de nuit. Jean y bascule le temps de réorganiser sa mémoire, puis revient au preset d'avant (sauf si le preset a été changé entre-temps). Auparavant, la consolidation tournait sur le preset laissé actif la veille, parfois un modèle rapide moins capable de réorganiser la mémoire. Si la bascule échoue, la consolidation est reportée d'une heure au lieu de tourner sur le mauvais modèle.
- **« Lit le message… » pendant la lecture du prompt.** Jean affichait « Réfléchit… » dès l'envoi, alors que le modèle lisait encore le message. « Réfléchit… » n'apparaît plus qu'avec le raisonnement.
- **Compteur de compactages masqué.** La conversation unique de Jean est compactée en continu : le compteur n'apportait rien en mode Jean.

## Corrections

- **Jean disparaissait** une à deux secondes après des clics rapides entre la scène plein écran et la conversation. La copie animée qui vole d'une place à l'autre héritait de l'état caché du personnage ; elle reste désormais visible et repart de sa position à l'écran.
- **Images de la bulle plein écran.** Elles n'ouvraient pas la visionneuse au clic, apparaissaient cassées le temps du chargement puis surgissaient d'un coup, et se rechargeaient pendant l'écriture de la réponse. Elles sont maintenant cliquables, un emplacement animé les remplace pendant le chargement, elles arrivent en fondu, et une image déjà chargée n'est plus recréée.
- **Glisser-déposer des presets.** La copie qui suit la souris apparaissait à moitié transparente et décalée du curseur. Elle est désormais pleine et suit le curseur.

## Mise à jour

    ajean update

Vérifié par les tests automatiques et sur le serveur Linux de test. Non testé : une consolidation de nuit réelle avec un preset du sommeil différent du preset actif, et le glisser-déposer des presets sur tous les navigateurs.
