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
func skillsSystemPrompt(caps Caps) string {
	if !caps.Agent {
		return ""
	}
	list := ListSkills()
	var b strings.Builder
	b.WriteString("Skill tips: the first content line is a short title (#) used as the description. Only use read when the question concerns a skill listed below — otherwise don't call the tool.\n")
	if len(list) == 0 {
		b.WriteString("Skills: none yet.")
		return b.String()
	}
	b.WriteString("Skills:\n")
	for _, s := range list {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Desc)
	}
	return strings.TrimRight(b.String(), "\n")
}
