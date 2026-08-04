## Corrections

**L'IA déposait ses fichiers sur votre Bureau.** En mode agent, quand elle créait un fichier (une recherche web mise de côté, un script, des notes), celui-ci atterrissait dans le dossier depuis lequel AJEAN avait été lancé. Le Bureau si vous aviez double-cliqué le fichier téléchargé, `C:\ProgramData\jean\bin` si vous passiez par l'installation. Personne ne s'attend à ce qu'une conversation laisse des fichiers derrière elle.

L'IA travaille désormais dans un dossier à elle, `workspace`, rangé avec vos réglages. Les fichiers déjà créés ne bougent pas, et le bouton « Où sont mes fichiers ? » vous dit où regarder. Si vous lui demandez explicitement d'écrire à un endroit précis, elle le fait toujours : seuls les fichiers sans destination indiquée sont rangés.

Merci à Sébastien pour le signalement, et pour avoir tout de suite pointé le lien avec le dossier de lancement.

**Le bouton de mise à jour échouait sur un message incompréhensible.** Sous Linux, lancé sans `sudo`, il répondait `open /usr/local/bin/.jean-update.tmp: permission denied`. AJEAN prévoyait bien un message expliquant qu'il fallait des privilèges, mais il arrivait trop tard dans le déroulé : l'échec se produisait avant, au moment d'écrire le fichier temporaire.

Les droits sont maintenant vérifiés avant de télécharger quoi que ce soit, et le message donne la commande exacte à lancer. Le téléchargement dispose par ailleurs de dix minutes au lieu de trente secondes : sur une connexion lente, une mise à jour qui réussissait pouvait être annoncée comme un échec.

Merci à Emmanuel pour le signalement et pour avoir isolé le rôle de `sudo`.

## Lancement sous Windows

**On sait enfin ce que fait le fichier téléchargé.** Le double-clic sur `jean-windows-amd64.exe` déclenchait une installation silencieuse : copie du programme, ajout au PATH, sans le moindre message. Impossible de savoir si l'on venait d'installer AJEAN ou seulement de l'ouvrir. Deux copies du programme coexistaient alors, celle que vous lanciez et celle installée, et la mise à jour ne touchait que la première.

AJEAN vous pose maintenant la question, une seule fois. Il annonce où il s'installe, où vont vos réglages, ajoute un raccourci au menu Démarrer et au Bureau, puis démarre depuis la copie installée. Un seul programme, à un endroit connu, que la mise à jour retrouve. Répondre non le lance sans rien installer. La désinstallation retire les raccourcis qu'elle a posés.

## Interface

**« Où sont mes fichiers ? »** Un bouton dans les actions affiche l'emplacement de vos données, de la configuration, de la mémoire et du dossier de travail de l'IA. Il vous prévient aussi si le programme que vous exécutez n'est pas celui installé. En ligne de commande : `jean where`.

**Nouvelle icône.** L'icône de l'application, celle de la zone de notification Windows et celle de la barre de menus macOS reprennent le logo noir du site. Elles sont dessinées à partir d'une source unique et ne peuvent plus se retrouver décalées les unes des autres. Sur macOS, l'icône s'adapte au thème clair ou sombre de la barre de menus.

## Mise à jour

```
jean update
```

Si vous lancez AJEAN sans privilèges administrateur et que le programme appartient à root, cette version-ci doit encore être installée en ligne de commande avec `sudo jean update`. Les suivantes vous le diront clairement depuis l'interface.

L'icône de la barre de menus macOS n'a pas pu être vérifiée sur une vraie machine.
