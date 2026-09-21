Cette version ajoute un mode CODER activable par projet et corrige, sous Windows, l'échec silencieux des boutons de démarrage et d'arrêt du moteur.

## Mode CODER par projet

Chaque projet peut désormais passer en mode CODER, indépendamment des autres, depuis le menu d'options du projet (menu à trois points, entrée « Options »). Activé, il ajoute au prompt système un cadre d'ingénierie strict, orienté fiabilité sur une base de code : cartographier le projet avant d'agir, réutiliser l'existant, faire la plus petite modification possible, puis vérifier qu'on n'a rien cassé et rapporter honnêtement ce qui a été vérifié. Un badge « CODER » s'affiche au-dessus de la zone de saisie quand le mode est actif. Le cadre ne s'applique qu'en mode agent, là où le modèle dispose réellement d'outils.

## Windows : erreur du moteur enfin visible

Sous Windows, les boutons Démarrer, Arrêter et Redémarrer du moteur pouvaient échouer sans que l'interface n'affiche la moindre raison : elle montrait seulement un bref message d'action, alors que le modèle ne se chargeait pas. La cause côté serveur était souvent un journal du moteur devenu la propriété d'une session administrateur, que le processus courant ne pouvait plus ouvrir en écriture, ce qui interrompait tout le démarrage. Deux corrections sont apportées :

- L'interface remonte maintenant l'erreur renvoyée par le serveur dans une boîte de dialogue, au lieu de l'ignorer.
- Le démarrage du moteur ne dépend plus de l'ouverture du journal : si le fichier principal n'est pas accessible en écriture, l'écriture bascule sur un emplacement temporaire, et à défaut le moteur démarre sans journal redirigé plutôt que d'échouer. L'enregistrement du fichier de PID devient également non bloquant.

## Mise à jour

    ajean update
