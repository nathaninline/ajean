L'IA peut maintenant agir sur un autre PC que le serveur : un petit client à installer, et tu choisis la machine sur laquelle elle travaille.

## Piloter un autre PC depuis l'IA

Parfois la puissance est sur le serveur, mais ce que tu veux faire est ailleurs — sur ton portable, un poste de travail, une autre machine. AJEAN sait maintenant s'y étendre.

Sur le PC à piloter, tu installes **ajean-remote** : un client minuscule et séparé (quelques mégaoctets, aucune interface ni moteur), qui ne fait qu'une chose — ouvrir une connexion **sortante** vers ton serveur et exécuter ce que l'IA lui demande. Aucun port à ouvrir, rien à configurer dans la box.

Dans l'interface, une section **Postes distants** : tu génères un code d'appairage, tu choisis les capacités autorisées (shell, lecture, écriture, listing) et un dossier de travail. Sur l'autre PC :

    ajean-remote install https://ton-serveur --code TONCODE --allow shell,read,write,list

Le client se connecte, s'installe en service (invisible, au démarrage), et se reconnecte tout seul.

## Tu choisis la machine cible

Pas de nouveaux outils à apprendre pour l'IA : elle garde son shell, son écriture et son édition de fichiers. Un sélecteur **« L'IA agit sur : Ce serveur / <ton poste> »** décide simplement *où* ces outils s'exécutent. Tu bascules d'un clic. Et si le poste choisi est hors ligne, l'action échoue proprement plutôt que de retomber par erreur sur le serveur.

## Pensé pour ne pas se retourner contre toi

Le poste se connecte en sortant, sa clé d'appareil est stockée hachée côté serveur, et c'est **toi** qui décides, machine par machine, ce que l'IA a le droit de faire et dans quel dossier. Lecture et écriture sont confinées à ce dossier ; les capacités effectives sont l'intersection de ce que tu autorises et de ce que le poste accepte localement.
