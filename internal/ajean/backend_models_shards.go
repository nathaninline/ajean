package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Modèles découpés en plusieurs fichiers. Au-delà d'une cinquantaine de Go, un
// dépôt Hugging Face publie son GGUF en tranches nommées par convention
// llama.cpp : `<base>-00001-of-00003.gguf`, `…-00002-of-00003.gguf`, etc.
// llama-server n'a besoin QUE de la première : il ouvre les suivantes tout seul,
// à condition qu'elles soient dans le même dossier.
//
// Tout le reste d'AJEAN traitait chaque tranche comme un modèle indépendant :
// le lien collé ne rapatriait qu'un fichier sur trois (le moteur mourait sur
// « tensor not found »), la liste du sélecteur montrait trois entrées dont deux
// ne démarrent pas, et supprimer « le modèle » laissait les deux autres tranches
// occuper le disque. Ce fichier ramène la tranche au rang de détail : une
// famille de tranches = UN modèle, désigné par sa première.

// shardRe reconnaît le suffixe de tranche en fin de nom de fichier. Le nombre de
// chiffres n'est pas figé à 5 : le convertisseur de llama.cpp en pose 5, mais
// des dépôts en publient avec moins.
var shardRe = regexp.MustCompile(`(?i)-(\d{2,5})-of-(\d{2,5})\.gguf$`)

// shardInfo décompose un nom de tranche : rang (1-based) et nombre total. ok
// vaut false pour un modèle d'un seul fichier.
func shardInfo(name string) (idx, total int, ok bool) {
	m := shardRe.FindStringSubmatch(baseName(name))
	if m == nil {
		return 0, 0, false
	}
	idx, _ = strconv.Atoi(m[1])
	total, _ = strconv.Atoi(m[2])
	if idx < 1 || total < 1 || idx > total {
		return 0, 0, false // numérotation incohérente : on le traite en fichier simple
	}
	return idx, total, true
}

// shardNameAt renvoie le nom de la tranche de rang i dans la même famille, en
// conservant la largeur de la numérotation d'origine (00002, pas 2).
func shardNameAt(name string, i int) string {
	m := shardRe.FindStringSubmatchIndex(name)
	if m == nil {
		return name
	}
	width := m[3] - m[2]
	totalPart := name[m[4]:m[5]]
	return name[:m[2]] + fmt.Sprintf("%0*d", width, i) + "-of-" + totalPart + name[m[5]:]
}

// isFollowerShard : tranche 2..N, celles qu'on ne montre jamais comme un modèle
// à part entière (elles ne démarrent pas seules).
func isFollowerShard(name string) bool {
	idx, _, ok := shardInfo(name)
	return ok && idx > 1
}

// shardFamily renvoie les noms de TOUTES les tranches de la famille de name (y
// compris name), dans l'ordre. Un fichier simple se renvoie lui-même.
func shardFamily(name string) []string {
	base := baseName(name)
	_, total, ok := shardInfo(base)
	if !ok {
		return []string{base}
	}
	out := make([]string, 0, total)
	for i := 1; i <= total; i++ {
		out = append(out, shardNameAt(base, i))
	}
	return out
}

// shardFamilySize additionne la taille des tranches PRÉSENTES dans dir. Sert à
// afficher « 42 Go » sur la première tranche au lieu des 15 Go qu'elle pèse
// seule, ce qui laissait croire à un modèle trois fois plus petit.
func shardFamilySize(dir, name string) int64 {
	var sum int64
	for _, n := range shardFamily(name) {
		if st, err := os.Stat(filepath.Join(dir, n)); err == nil && !st.IsDir() {
			sum += st.Size()
		}
	}
	return sum
}

// shardFamilyMissing liste les tranches ABSENTES du dossier. Un modèle dont il
// manque une tranche démarre puis meurt sur une erreur de tenseur introuvable :
// autant le dire dans la liste plutôt que dans les logs du moteur.
func shardFamilyMissing(dir, name string) []string {
	var missing []string
	for _, n := range shardFamily(name) {
		if st, err := os.Stat(filepath.Join(dir, n)); err != nil || st.IsDir() {
			missing = append(missing, n)
		}
	}
	return missing
}

// shardURLSet transforme l'URL d'UNE tranche en la liste complète des URLs de la
// famille, dans l'ordre, avec les noms de fichiers correspondants. Le lien collé
// peut désigner n'importe quelle tranche (on tombe souvent sur la deuxième en
// parcourant l'arborescence du dépôt) : on remonte toujours à la première.
func shardURLSet(dlURL, name string) (urls, names []string) {
	fam := shardFamily(name)
	if len(fam) == 1 {
		return []string{dlURL}, fam
	}
	base := baseName(name)
	for _, n := range fam {
		// Remplacement du seul nom de fichier, en fin d'URL : le nom peut réapparaître
		// ailleurs dans le chemin (dossier de quantification portant le même libellé),
		// et un ReplaceAll y toucherait aussi.
		i := strings.LastIndex(dlURL, base)
		if i < 0 {
			return []string{dlURL}, []string{base} // lien inattendu : on ne devine rien
		}
		urls = append(urls, dlURL[:i]+n+dlURL[i+len(base):])
		names = append(names, n)
	}
	return urls, names
}
