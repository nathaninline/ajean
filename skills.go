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
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`Tu as accès à des skills (guides spécialisés). Pour lire le contenu détaillé d'un skill quand c'est pertinent, appelle la fonction read_skill(name="<nom>"). N'appelle pas read_skill si la question est sans rapport avec un skill listé.

Skills disponibles :
`)
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
