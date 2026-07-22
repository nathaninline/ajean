# assemble.ps1 — reconstruit ui/index.html à partir des sources ui/src/.
#
# ui/index.html est un fichier GÉNÉRÉ (mais committé : go:embed et
# ajean-app/build-server-ui.ps1 le lisent tel quel). Pour modifier l'UI :
# éditer ui/src/ (styles.css, js/NN-*.js, index.tmpl.html) puis relancer ce
# script. Les js/ sont concaténés dans l'ordre alphabétique (préfixes NN-),
# tout reste en scope global comme avant le découpage.
#
# Tout est manipulé en octets bruts (fins de ligne/encodage préservés).

$ErrorActionPreference = 'Stop'
$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$src  = Join-Path $here 'src'
$dst  = Join-Path $here 'index.html'

$tmpl = [IO.File]::ReadAllText((Join-Path $src 'index.tmpl.html'))
$css  = [IO.File]::ReadAllText((Join-Path $src 'styles.css'))
$js   = (Get-ChildItem (Join-Path $src 'js') -Filter '*.js' | Sort-Object Name |
         ForEach-Object { [IO.File]::ReadAllText($_.FullName) }) -join ''

foreach ($m in '@@CSS@@', '@@JS@@') {
  if (-not $tmpl.Contains("$m`n")) { throw "marqueur $m introuvable dans index.tmpl.html" }
}
$html = $tmpl.Replace("@@CSS@@`n", $css).Replace("@@JS@@`n", $js)

[IO.File]::WriteAllText($dst, $html, [Text.UTF8Encoding]::new($false))
Write-Host "OK -> $dst ($([IO.File]::ReadAllBytes($dst).Length) octets)"
