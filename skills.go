package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Skill struct {
	Name    string
	Desc    string // first non-empty line, with leading #'s stripped
	Content string
}

func skillsEnabled() bool {
	_, err := os.Stat(skillsFlag())
	return err == nil
}

func setSkillsEnabled(on bool) error {
	_ = os.MkdirAll(skillsDir(), 0o755)
	if on {
		f, err := os.Create(skillsFlag())
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(skillsFlag()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListSkills walks SKILLS/<name>/SKILL.md, returning name+desc+full content.
func ListSkills() []Skill {
	entries, err := os.ReadDir(skillsDir())
	if err != nil {
		return nil
	}
	out := []Skill{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		f := filepath.Join(skillsDir(), e.Name(), "SKILL.md")
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := string(b)
		desc := ""
		for _, line := range strings.Split(content, "\n") {
			s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
			if s != "" {
				desc = s
				break
			}
		}
		out = append(out, Skill{Name: e.Name(), Desc: desc, Content: content})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safeSkillDir validates and returns the on-disk path for a skill directory.
func safeSkillDir(name string) (string, error) {
	if !skillNameRe.MatchString(name) {
		return "", fmt.Errorf("nom invalide (alphanum, ._-)")
	}
	root, err := filepath.Abs(skillsDir())
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, name)
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("path invalide")
	}
	return abs, nil
}

// SkillContent loads SKILL.md for the given name (used by the read_skill tool).
func SkillContent(name string) string {
	d, err := safeSkillDir(name)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(d, "SKILL.md"))
	if err != nil {
		return ""
	}
	return string(b)
}

func SaveSkill(name, old, content string) error {
	if old != "" && old != name {
		if od, err := safeSkillDir(old); err == nil {
			_ = os.RemoveAll(od)
		}
	}
	d, err := safeSkillDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(content), 0o644)
}

// AppendSkill ajoute du contenu à la fin de SKILL.md (le crée si absent).
// C'est le mode "offset" : l'IA peut consigner une solution trouvée sans
// réécrire tout le skill.
func AppendSkill(name, content string) error {
	d, err := safeSkillDir(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	p := filepath.Join(d, "SKILL.md")
	existing, _ := os.ReadFile(p)
	var b strings.Builder
	if len(existing) > 0 {
		b.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(strings.TrimRight(content, "\n"))
	b.WriteString("\n")
	return os.WriteFile(p, []byte(b.String()), 0o644)
}

func DeleteSkill(name string) error {
	d, err := safeSkillDir(name)
	if err != nil {
		return err
	}
	if _, err := os.Stat(d); err != nil {
		return fmt.Errorf("introuvable")
	}
	return os.RemoveAll(d)
}

// skillsSystemPrompt returns the lightweight skills directory message to
// prepend to the conversation when skills are enabled.
func skillsSystemPrompt() string {
	if !skillsEnabled() {
		return ""
	}
	list := ListSkills()
	var b strings.Builder
	b.WriteString(`Skills = guides Markdown que tu gères toi-même via le tool skill(action, name, content) : read=lire, write=créer/remplacer, append=ajouter à la fin sans réécrire, delete=supprimer. De ta propre initiative, crée ou enrichis un skill quand tu découvres une solution réutilisable (problème enfin résolu, procédure, astuce) ; sa 1re ligne est un titre court (#) servant de description. N'utilise read que si la question concerne un skill listé.`)
	if len(list) == 0 {
		b.WriteString("\nAucun skill pour l'instant.")
		return b.String()
	}
	b.WriteString("\nSkills :\n")
	for _, s := range list {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdSkills(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "on":
		if err := setSkillsEnabled(true); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " skills activés")
	case "off":
		if err := setSkillsEnabled(false); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " skills désactivés")
	case "", "list":
		state := dim("off")
		if skillsEnabled() {
			state = green("on")
		}
		fmt.Printf("%s  (%s)  état: %s\n", cyan("Skills"), skillsDir(), state)
		sk := ListSkills()
		if len(sk) == 0 {
			fmt.Printf("  (aucun — crée %s/<nom>/SKILL.md)\n", skillsDir())
			return nil
		}
		for _, s := range sk {
			fmt.Printf("  %s  %s\n", bold(s.Name), s.Desc)
		}
	default:
		return fmt.Errorf("usage: jean skills [on|off|list]")
	}
	return nil
}
