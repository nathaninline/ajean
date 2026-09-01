Descriptions de projet fournies à l'assistant, déplacement des conversations et des notes entre projets, et plusieurs corrections (date des conversations, interface figée sur mobile, moteur qui ne démarrait plus avec un template personnalisé).

## Nouveautés

* **Description de projet.** Chaque projet peut recevoir une description (menu du projet → « Décrire ») expliquant à quoi il sert : contexte, contraintes, ton attendu. Ce texte est fourni à l'assistant au début de chaque conversation du projet, pour qu'il sache d'emblée sur quoi il travaille sans qu'on ait à le lui réexpliquer.
* **Déplacer une conversation vers un autre projet.** Depuis le menu d'une conversation archivée (« Déplacer vers… »). La conversation en cours n'est pas déplaçable ; il faut en ouvrir une autre d'abord.
* **Déplacer une note de mémoire vers un autre projet.** Depuis la liste des pages de mémoire des réglages (bouton « déplacer »). Mémoire et conversations restent deux axes indépendants : déplacer un fil ne déplace pas les notes qu'il cite, et inversement.
* **Copie du journal du moteur.** Un bouton « copier » dans le panneau d'état (pastille en haut du menu) copie tout le journal, pour le coller dans un rapport ou un message.

## Corrections

* Rouvrir une ancienne conversation ne modifie plus sa date : la date affichée reflète désormais la dernière activité réelle, pas le moment de réouverture. La chronologie de l'historique redevient fiable.
* Sur mobile, revenir sur un onglet laissé en arrière-plan pendant une génération ne fige plus l'interface : le rattrapage du flux est rendu directement au lieu d'être ré-animé caractère par caractère, ce qui saturait le fil d'exécution jusqu'à ce qu'un rafraîchissement le débloque.
* Régler le niveau de réflexion depuis l'éditeur de preset alors qu'un template de chat personnalisé était configuré pouvait corrompre les arguments de lancement (le drapeau `--jinja` se retrouvait à l'intérieur du chemin du template), empêchant le moteur de démarrer. Le découpage et la citation des arguments respectent désormais les guillemets.
