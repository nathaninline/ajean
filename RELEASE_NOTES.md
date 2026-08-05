Cette version donne à l'IA l'accès à internet sans rien installer. Jusqu'ici il fallait héberger un serveur Crawl4AI — un conteneur Docker, un Chromium, quelques centaines de mégaoctets — avant que l'IA puisse chercher ou lire quoi que ce soit sur le web. C'était une barrière que peu de gens franchissaient. AJEAN embarque maintenant son propre moteur web.

## Internet fonctionne dès l'installation

Le nouveau moteur est écrit en Go et vit dans le binaire. Il n'y a rien à télécharger, rien à lancer, aucune adresse de serveur à renseigner : vous activez l'accès internet, et l'IA dispose de `web_search`, `web_open`, `web_read` et `web_grep`.

Il récupère les pages, en extrait le contenu réel — en écartant les menus, les bandeaux et les pieds de page — puis le convertit en texte lisible. La recherche passe par DuckDuckGo, dont les résultats sont désormais lus directement dans la structure de la page plutôt que devinés dans du texte reformaté : les titres, les adresses et les extraits sont plus fiables qu'avant.

## Crawl4AI reste disponible pour les pages en JavaScript

Le moteur intégré lit les pages telles que le serveur les envoie, sans exécuter leur JavaScript. C'est sans conséquence pour la documentation, les articles, les blogs, Wikipédia, les dépôts de code ou les forums, qui représentent l'essentiel de ce qu'une IA a besoin de consulter. En revanche, une application web dont le contenu n'apparaît qu'une fois le JavaScript exécuté restera illisible.

Pour ces cas-là, le moteur Crawl4AI est toujours là, avec son navigateur complet. Le choix se fait dans le panneau « Accès internet » de l'interface, ou en ligne de commande :

```
ajean internet engine go
ajean internet engine crawl4ai
```

Les installations qui utilisaient déjà un serveur Crawl4AI le gardent : rien ne change pour elles tant qu'elles n'ont pas choisi l'autre moteur.

## L'IA sait maintenant quand une page lui est inaccessible

Une page vide ne dit rien de ce qui s'est passé. L'IA en concluait qu'elle avait mal lu, et rouvrait la même adresse encore et encore.

Quand une page arrive bien mais ne contient presque aucun texte, AJEAN reconnaît la signature d'un contenu affiché par JavaScript et l'explique à l'IA, en lui demandant de chercher ailleurs plutôt que d'insister. Les pages courtes mais légitimes, elles, continuent d'être lues normalement.

Dans le même esprit, l'IA ne se voit plus proposer d'agir sur une page — dérouler une section, fermer un bandeau, attendre un élément — quand le moteur intégré est actif, puisqu'il ne peut pas le faire. Elle ne perd plus de temps à essayer.

## Mise à jour

```
ajean update
```

Ou le bouton de l'interface.
