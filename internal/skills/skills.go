// Package skills implements Agent Skills: small, named, on-demand capability
// packets stored as skills/<name>/SKILL.md files. Discovery reads only each
// skill's name + description into a catalog (see Catalog); the full body is
// loaded later, on demand, by the skill tool. This progressive disclosure keeps
// the system prompt small even when many skills are installed.
//
// Skill files are local and user-authored, so their bodies are trusted content
// (the same trust class as AGENTS.md/CLAUDE.md), unlike UNTRUSTED web content.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill is one discovered SKILL.md. Dir is the skill's folder so a body can
// reference bundled files (which the model then reads with read_file).
type Skill struct {
	Name        string
	Description string
	Body        string
	Dir         string
}

// Discover finds skills in a global scope (globalDir, typically ~/.claude) and a
// project scope (projectDir, typically the working directory), each scanning
// <dir>/skills/*/SKILL.md. Global skills are found first; a project skill with
// the same Name overrides the global one. A missing skills/ directory is normal
// (yields nothing); only a real read failure returns an error. Files lacking a
// valid name+description are skipped rather than failing the whole discovery.
//
// The returned slice is ordered global-first, then project, with overridden
// global entries removed.
func Discover(globalDir, projectDir string) ([]Skill, error) {
	global, err := discoverDir(globalDir)
	if err != nil {
		return nil, err
	}
	project, err := discoverDir(projectDir)
	if err != nil {
		return nil, err
	}

	// Project overrides global by name.
	overridden := make(map[string]bool, len(project))
	for _, s := range project {
		overridden[s.Name] = true
	}

	out := make([]Skill, 0, len(global)+len(project))
	for _, s := range global {
		if !overridden[s.Name] {
			out = append(out, s)
		}
	}
	out = append(out, project...)
	return out, nil
}

// discoverDir scans dir/skills/*/SKILL.md and returns the valid skills it finds.
// An empty dir yields nothing.
func discoverDir(dir string) ([]Skill, error) {
	if dir == "" {
		return nil, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "skills", "*", "SKILL.md"))
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read skill %s: %w", path, err)
		}
		name, desc, body, ok := parseFrontmatter(string(data))
		if !ok {
			continue // skip files without a usable name + description
		}
		out = append(out, Skill{
			Name:        name,
			Description: desc,
			Body:        body,
			Dir:         filepath.Dir(path),
		})
	}
	return out, nil
}

// parseFrontmatter extracts name and description from a leading YAML-style
// frontmatter block and returns the body that follows it. The format is a "---"
// fence, key: value lines, a closing "---" fence, then the body:
//
//	---
//	name: changelog
//	description: Generate a changelog from git log.
//	---
//	<body>
//
// It is deliberately minimal (no third-party YAML): only top-level key: value
// pairs are read and surrounding quotes are trimmed. ok is false when there is
// no frontmatter or when name/description are missing.
func parseFrontmatter(content string) (name, description, body string, ok bool) {
	// Normalize CRLF so fence detection is robust on Windows-authored files.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", "", false
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", "", "", false // unterminated frontmatter
	}

	for _, line := range lines[1:end] {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = trimQuotes(strings.TrimSpace(value))
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	if name == "" || description == "" {
		return "", "", "", false
	}

	body = strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	return name, description, body, true
}

// trimQuotes removes a single pair of matching surrounding quotes, if present.
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Catalog renders the AGENT SKILLS prompt section body: one "- name: description"
// line per skill, in discovery order. It returns "" when there are no skills, so
// the caller can omit the section entirely.
func Catalog(sk []Skill) string {
	if len(sk) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range sk {
		b.WriteString("- ")
		b.WriteString(s.Name)
		b.WriteString(": ")
		b.WriteString(s.Description)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
