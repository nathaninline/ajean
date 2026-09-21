Cette version affine le mode CODER et améliore l'affichage des résultats d'outils dans le chat.

## Mode CODER plus fiable

Le mode CODER pouvait rester plusieurs minutes sans rien produire, ou entreprendre des actions qui n'avaient pas été demandées (par exemple pousser du code sur un dépôt). La cause venait de son cadre système, à la fois trop verbeux (ce qui fait sur-raisonner le modèle au lieu d'agir) et trop permissif sur les opérations sensibles. Le cadre a été resserré :

- Des règles d'action courtes et concrètes, sans consignes sur la façon de réfléchir, pour que le modèle agisse au lieu de délibérer sans fin.
- Une modification reste petite en portée mais complète : ce que le changement impacte ailleurs (appelants, cas liés) est traité aussi, pas laissé à moitié fait.
- Aucune opération de publication ou de déploiement sans demande explicite : pas de commit, push, reset ou rebase, pas de release, de déploiement, de redémarrage de service ni de remplacement de binaire. L'inspection en lecture seule (statut, diff, journal git) reste autorisée.

## Résultats d'outils : « voir plus » à côté de « copier »

Dans un résultat d'outil tronqué, le bouton « voir plus » occupait une ligne séparée sous le bloc, alors que « copier » flottait dans le coin. Les deux boutons sont désormais réunis dans une barre en bas à droite du résultat, avec le même style, ce qui évite la ligne supplémentaire.

Le bouton « voir plus » dépliait le contenu sans que rien ne semble changer : le texte complet était bien chargé, mais le bloc restait plafonné en hauteur, obligeant à faire défiler à l'intérieur. Au dépliage, le bloc lève maintenant ce plafond et affiche réellement l'ensemble du résultat.

## Mise à jour

    ajean update
