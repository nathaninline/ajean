// Point d'entrée du binaire ajean. Tout le code vit dans internal/jean ; ce
// dossier ne porte que le main() et les ressources Windows (.syso, icône,
// versioninfo) qui doivent résider dans le dossier du package main.
//
// Les directives ci-dessous embarquent les métadonnées Windows (éditeur, version,
// description) dans le .exe pour réduire les faux positifs antivirus. Régénère
// les .syso après avoir bumpé la version : `go generate ./...`
// (nécessite : go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest)
//
//go:generate go run github.com/nathaninline/ajean/tools/gen-icon icon.ico
//go:generate goversioninfo -64 -icon=icon.ico -o resource_windows_amd64.syso versioninfo.json
//go:generate goversioninfo -64 -arm -icon=icon.ico -o resource_windows_arm64.syso versioninfo.json
package main

import "github.com/nathaninline/ajean/internal/ajean"

func main() { ajean.Main() }
