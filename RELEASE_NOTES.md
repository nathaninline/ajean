Correctif pour Windows : Microsoft Defender supprimait AJEAN.

## Windows

- **AJEAN supprimé par Defender (Trojan:Win32/Bearfoos.A!ml).** Depuis la 0.17.5, Defender pouvait classer AJEAN comme cheval de Troie juste après une mise à jour, puis supprimer le programme et ses raccourcis. Il s'agit d'un faux positif. La 0.17.7 l'aggravait : pour fermer le navigateur piloté par l'IA en même temps qu'AJEAN, ce navigateur était lancé suspendu puis relancé par un appel système bas niveau, une méthode qui ressemble à une injection de processus. Il est désormais lancé normalement, et sa fermeture avec AJEAN est conservée.
- **Les données n'ont pas été perdues.** Defender ne retire que le programme (`ajean.exe`) et ses raccourcis. Conversations, mémoire, presets et modèles restent dans `C:\ProgramData\ajean`.

## Récupérer AJEAN après une suppression

1. Sécurité Windows, Protection contre les virus et menaces, Historique de protection.
2. Ouvrir l'alerte Bearfoos, puis Actions, Restaurer (ou télécharger à nouveau AJEAN depuis ajean.app).
3. Pour éviter une nouvelle alerte : Gérer les paramètres, Exclusions, ajouter le dossier `C:\ProgramData\ajean`.

## Mise à jour

    ajean update

Vérifié par les tests automatiques et un scan Defender. La fermeture du navigateur piloté avec AJEAN n'a pas été retestée sous Windows.
