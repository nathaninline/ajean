Cette version fiabilise le mode agent sous Windows.

## Les commandes shell ne se cassent plus sur les guillemets et les accents

Sous Windows, en mode agent, les commandes partaient à cmd.exe en un seul bloc (`cmd /C "..."`). Or cmd.exe a des règles de guillemets impitoyables : dès qu'une commande contenait des guillemets imbriqués, des caractères spéciaux (`& | < >`) ou des accents, elle se cassait. Le modèle s'en sortait en tâtonnant (il réessayait en PowerShell, puis via un fichier Python), ce qui allongeait chaque réponse de raisonnements inutiles.

Désormais la commande est écrite dans un petit fichier de commandes temporaire, exécuté proprement : l'analyseur de cmd.exe est bien plus tolérant ligne par ligne, l'encodage UTF-8 est forcé (les accents passent), le code de sortie est préservé, et les commandes sur plusieurs lignes fonctionnent enfin. Le fichier temporaire est nettoyé tout seul après chaque commande. Rien ne change sous Linux et macOS.

## Mise à jour

```
ajean update
```

Merci à Cal pour le signalement détaillé et la piste de correction. Corrigé et vérifié directement sous Windows.
