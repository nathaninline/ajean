Version de finition de l'éditeur de preset et du GPU Cloud.

## GPU Cloud

- **Première utilisation** : pendant qu'AJEAN installe son composant GPU Cloud, un bloc « Préparation du GPU Cloud » affiche l'étape en cours, au lieu d'une ligne en petits caractères. En cas d'échec, la raison s'affiche avec un bouton « Réessayer ».
- **Aucun compte connecté** : l'éditeur propose directement « Connecter un compte Modal », champ et bouton ouverts, au lieu d'une liste vide et d'un lien « connecter un autre compte ».
- La carte GPU de la zone « Appareil » s'intitule simplement « Modal A100-40GB », sans la mention « cloud ».
- « GPU Cloud » prend une majuscule.

## Éditeur de preset

- **Listes de sélection** : chaque liste prend la largeur de la valeur choisie et se cale au bord droit, alignée avec les nombres et les interrupteurs. Il n'y a plus d'espace vide entre la valeur et sa flèche, et les libellés ne sont plus écrasés. Sur iPhone, la largeur est ajustée de la même façon.
- **Liste des modèles** : les projecteurs vision (mmproj) n'y apparaissent plus, ils ont leur propre choix dans « Vision ». Un mmproj choisi par erreur comme modèle reste affiché pour pouvoir être corrigé.
- Sous-titre du raisonnement non réglé raccourci : « non réglé : le modèle décide ».

## Accès distant

Le bandeau « le serveur n'est pas à jour » affiché sur ajean.link ne propose plus de l'ignorer : il reste visible, avec son bouton de mise à jour, tant que le serveur n'est pas à jour.

## Mise à jour

    ajean update

Vérifié sous Windows et sur un serveur Linux. Non testé : macOS, et l'affichage sur un iPhone réel (ajustement des listes vérifié par simulation).
