Correctif de l'accès OpenAI public (gateway par machine).

## Passerelle OpenAI injoignable

L'endpoint public d'une machine (adresse en oai.ajean.link) pouvait devenir injoignable, avec des connexions qui expiraient sans réponse. En cause : l'obtention du certificat Let's Encrypt échouait quand le service tournait sous un compte non privilégié, car la bibliothèque de certificats tentait d'ouvrir un port réservé (443) pour le challenge, ce qui était refusé.

Le challenge de validation est désormais résolu sur un port non privilégié en local, tandis que la vraie validation continue de passer par le tunnel. Les certificats s'obtiennent et se renouvellent sans privilège particulier, y compris pour un nouveau nom de machine.

## Mise à jour

```
ajean update
```

Vérifié sur le serveur de référence : émission du certificat réussie et endpoint /v1 de nouveau joignable (réponse normale après authentification).
