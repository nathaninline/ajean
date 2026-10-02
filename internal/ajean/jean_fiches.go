package ajean

// jean_fiches.go — 3ᵉ rangement de la mémoire de Jean : les FICHES (procédures,
// recettes, notices, guides de style…). Même principe que les skills : le NOM et
// une ligne « quand s'en servir » sont toujours sous les yeux de Jean (quelques
// tokens par fiche, dans le bloc Jean memory) ; le CONTENU, lui, n'entre dans le
// contexte que quand Jean l'ouvre avec jean_read. Un petit modèle oublie souvent
// de chercher : voir que la fiche existe est ce qui lui fait penser à l'ouvrir.
//
// Une fiche = memory/_jean/fiche-<nom>.md (à plat, comme le reste : chiffrement
// et sauvegarde la prennent en charge). Format :
//
//	# <nom>
//	when: <quand s'en servir>
//
//	<contenu>

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	jeanFichePrefix     = "fiche-"
	jeanFicheMaxChars   = 20000
	jeanFicheWhenMax    = 160
	jeanFicheIndexLimit = 100 // au-delà, l'index n'affiche que les plus récentes (le reste par jean_search)
)

var jeanFicheNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

type jeanFiche struct {
	Name, When, Content string
	ModTime             int64 // epoch ms
}

// normFicheName : « Déployer AJEAN » → « deployer-ajean ».
func normFicheName(n string) string {
	n = foldSearch(n)
	var b strings.Builder
	dash := false
	for _, r := range n {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func ficheFile(name string) string { return jeanFichePrefix + name + ".md" }

func parseFiche(name, s string) jeanFiche {
	f := jeanFiche{Name: name}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	i := 0
	if i < len(lines) && strings.HasPrefix(lines[i], "# ") {
		i++
	}
	if i < len(lines) && strings.HasPrefix(lines[i], "when: ") {
		f.When = strings.TrimSpace(strings.TrimPrefix(lines[i], "when: "))
		i++
	}
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	f.Content = strings.TrimRight(strings.Join(lines[i:], "\n"), "\n")
	return f
}

// jeanFiches liste les fiches (sans le contenu si full=false), récentes d'abord.
func jeanFiches(full bool) []jeanFiche {
	ents, _ := os.ReadDir(jeanDir())
	var out []jeanFiche
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, jeanFichePrefix) || !strings.HasSuffix(n, ".md") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(n, jeanFichePrefix), ".md")
		s, err := jeanRead(n)
		if err != nil {
			continue // chiffrée et verrouillée
		}
		f := parseFiche(name, s)
		if info, err := e.Info(); err == nil {
			f.ModTime = info.ModTime().UnixMilli()
		}
		if !full {
			f.Content = ""
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ModTime > out[j].ModTime })
	return out
}

// JeanSave crée ou REMPLACE une fiche (même nom = nouvelle version).
func JeanSave(name, when, content string) (string, error) {
	name = normFicheName(name)
	if !jeanFicheNameRe.MatchString(name) {
		return "", fmt.Errorf("nom invalide : court, ex. « deployer-ajean »")
	}
	when = strings.Join(strings.Fields(when), " ")
	if when == "" {
		return "", fmt.Errorf("'when' obligatoire : une ligne qui dit quand se servir de cette fiche")
	}
	if len([]rune(when)) > jeanFicheWhenMax {
		return "", fmt.Errorf("'when' trop long (%d car. max)", jeanFicheWhenMax)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("contenu vide")
	}
	if n := len([]rune(content)); n > jeanFicheMaxChars {
		return "", fmt.Errorf("fiche trop longue (%d car., max %d) : découpe-la en plusieurs fiches", n, jeanFicheMaxChars)
	}
	jeanMu.Lock()
	defer jeanMu.Unlock()
	old, _ := jeanRead(ficheFile(name))
	if err := jeanWrite(ficheFile(name), "# "+name+"\nwhen: "+when+"\n\n"+content+"\n"); err != nil {
		return "", err
	}
	if old != "" {
		_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » mise à jour (%s)", name, when))
		return fmt.Sprintf("[ok] fiche « %s » mise à jour", name), nil
	}
	_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » créée (%s)", name, when))
	return fmt.Sprintf("[ok] fiche « %s » enregistrée", name), nil
}

// JeanRead renvoie une fiche entière.
func JeanRead(name string) (string, error) {
	name = normFicheName(name)
	jeanMu.Lock()
	s, err := jeanRead(ficheFile(name))
	jeanMu.Unlock()
	if err != nil {
		return "", err
	}
	if s == "" {
		var names []string
		for _, f := range jeanFiches(false) {
			names = append(names, f.Name)
		}
		if len(names) == 0 {
			return "", fmt.Errorf("fiche « %s » introuvable : aucune fiche enregistrée", name)
		}
		return "", fmt.Errorf("fiche « %s » introuvable. Fiches : %s", name, strings.Join(names, ", "))
	}
	f := parseFiche(name, s)
	return "# " + f.Name + "\n(when: " + f.When + ")\n\n" + f.Content, nil
}

// jeanDeleteFiche supprime une fiche (trace gardée au journal).
func jeanDeleteFiche(name string) error {
	name = normFicheName(name)
	jeanMu.Lock()
	defer jeanMu.Unlock()
	p := filepath.Join(jeanDir(), ficheFile(name))
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("fiche « %s » introuvable", name)
	}
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + ".bak")
	_ = jeanAppendLocked("fiche", fmt.Sprintf("Fiche « %s » supprimée", name))
	return nil
}

// jeanFicheIndex : la liste injectée dans le bloc Jean memory.
func jeanFicheIndex() string {
	fs := jeanFiches(false)
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nYour fiches (procedures, how-tos, guides): when one matches the request, jean_read it FIRST and follow it.\n")
	for i, f := range fs {
		if i >= jeanFicheIndexLimit {
			fmt.Fprintf(&b, "(+%d older fiches: find them with jean_search)\n", len(fs)-i)
			break
		}
		b.WriteString("- " + f.Name + ": " + f.When + "\n")
	}
	return b.String()
}

// jeanFicheHits : fiches qui contiennent au moins la moitié des termes (pour jean_search).
func jeanFicheHits(terms []string) []string {
	var out []string
	for _, f := range jeanFiches(true) {
		hay := foldSearch(f.Name + " " + f.When + " " + f.Content)
		matched := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				matched++
			}
		}
		// Au moins la moitié des mots : une requête bavarde ne doit pas tout rater.
		if matched > 0 && matched*2 >= len(terms) {
			out = append(out, "- [fiche] "+f.Name+": "+f.When+" (jean_read to open)")
		}
	}
	return out
}
