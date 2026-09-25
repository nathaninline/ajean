Version de correction de la 0.16.0 : messages d'erreur du GPU cloud explicites et éditeur de preset plus lisible.

## GPU cloud

- **Crédit** : quand le crédit d'un compte Modal ne pouvait pas être lu, l'éditeur affichait seulement « crédit indisponible ». La vraie raison s'affiche désormais : facturation réservée au propriétaire du workspace Modal, connexion au compte expirée, compte introuvable sur la machine, ou le message de Modal lui-même.
- **Déploiement** : les erreurs de Modal ne s'affichent plus sous forme de cadre brut en anglais. Une limite de dépense atteinte (compte sans carte, par exemple) donne « limite de dépense atteinte : ajouter une carte ou relever la limite sur modal.com ».

## Éditeur de preset

- Les listes de sélection sont alignées à gauche, dans une colonne commune : les valeurs démarrent au même endroit d'une ligne à l'autre.
- La flèche des listes est dessinée et tombe exactement sur le bord droit, alignée avec les nombres et les interrupteurs.
- La ligne « Modèle de draft » suit la même mise en page que les autres.
- « API Externe » prend une majuscule.

## Mise à jour

    ajean update

Vérifié sous Windows. Non testé : Linux et macOS, et l'affichage de l'erreur de facturation d'un compte membre d'un workspace d'équipe (le message dépend de la réponse de Modal).
