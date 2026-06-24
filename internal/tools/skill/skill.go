// Package skill provides the skill tool. It lets the model load an Agent Skill's
// full instructions on demand: the system prompt lists only each skill's name and
// description (see internal/skills), and the model calls skill(<name>) to read the
// body when it decides the skill is relevant. SkillTool implements tools.Tool by
// structural typing, so this package does not import tools.
package skill

import (
	"fmt"
	"strings"

	"klauscode/internal/skills"
)

// SkillTool serves the bodies of discovered skills, keyed by name.
type SkillTool struct {
	bodies map[string]string
}

// New builds a skill tool from the discovered skills, indexing each body by name.
func New(sk []skills.Skill) *SkillTool {
	bodies := make(map[string]string, len(sk))
	for _, s := range sk {
		bodies[s.Name] = s.Body
	}
	return &SkillTool{bodies: bodies}
}

func (t *SkillTool) Name() string { return "skill" }

func (t *SkillTool) Description() string {
	return "skill(<name>): Load the full instructions for an available agent skill by name, e.g. skill(changelog). Use only names listed in the AGENT SKILLS section, then follow the instructions it returns."
}

// Call returns the named skill's body, lightly framed so the model can see where
// the skill instructions begin and end. An unknown name is returned as an error
// so the model can self-correct. The body is trusted, user-authored content.
func (t *SkillTool) Call(args string) (string, error) {
	name := strings.TrimSpace(args)
	body, ok := t.bodies[name]
	if !ok {
		return "", fmt.Errorf("unknown skill %q", name)
	}
	return fmt.Sprintf("--- skill: %s ---\n%s", name, body), nil
}
