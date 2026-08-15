Petite correction du bloc d'échantillonnage arrivé en 0.9.5.

## Présence et répétition, deux réglages distincts

Le champ « Pénalité de présence » portait le sous-titre « réduit les répétitions », ce qui prêtait à confusion : ce sont deux choses différentes. La présence pousse le modèle vers des mots nouveaux, la répétition est le vrai bouton anti-répétition. Le libellé est corrigé.

Et surtout, la pénalité de répétition manquait carrément à l'interface alors que le moteur savait déjà la recevoir. Un champ **Pénalité de répétition** rejoint donc le bloc Échantillonnage (valeur neutre : 1). Celui qui cherchait comment limiter les répétitions le trouvera maintenant.

## Mise à jour

```
ajean update
```

Merci à juanpa669 pour le signalement.
