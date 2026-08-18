Cette version corrige des problèmes remontés dans les issues GitHub : la détection Vulkan qui ratait les distributions type Fedora, et la doc de compilation sur macOS.

## Détection Vulkan (Fedora / Bazzite / RHEL)

* La détection du backend Vulkan ne regardait que le chemin Debian/Ubuntu (`/usr/lib/x86_64-linux-gnu/`). Sur Fedora, Bazzite et les dérivés RHEL, la bibliothèque vit dans `/usr/lib64/`, donc le GPU passait inaperçu et AJEAN retombait sur le CPU.
* AJEAN interroge maintenant `ldconfig` (la référence du système pour les bibliothèques) puis, en secours, teste plusieurs emplacements connus (`/usr/lib64`, `/lib64`, `/usr/lib`, Debian multiarch). Ton GPU Vulkan est reconnu quelle que soit la distribution.

## Compilation sur macOS

* Le README indiquait `CGO_ENABLED=0` pour compiler, ce qui casse le build sur macOS (le systray passe par Cocoa, qui exige CGO). La doc documente désormais les deux variantes : macOS sans ce drapeau (avec les Xcode Command Line Tools), Linux et Windows avec.

## Mise à jour

Depuis un terminal :

```
ajean update
```

Corrections de portabilité et de documentation, sans changement de comportement pour les installations existantes.
