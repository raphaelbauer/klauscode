package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates dir/skills/<name>/SKILL.md with the given content.
func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantOK   bool
		wantName string
		wantDesc string
		wantBody string
	}{
		{
			name:     "unquoted values",
			content:  "---\nname: greet\ndescription: Greet someone.\n---\nDo the greeting.",
			wantOK:   true,
			wantName: "greet",
			wantDesc: "Greet someone.",
			wantBody: "Do the greeting.",
		},
		{
			name:     "quoted values",
			content:  "---\nname: \"greet\"\ndescription: 'Greet someone.'\n---\nbody",
			wantOK:   true,
			wantName: "greet",
			wantDesc: "Greet someone.",
			wantBody: "body",
		},
		{
			name:     "crlf line endings",
			content:  "---\r\nname: greet\r\ndescription: Greet.\r\n---\r\nbody",
			wantOK:   true,
			wantName: "greet",
			wantDesc: "Greet.",
			wantBody: "body",
		},
		{
			name:    "missing description",
			content: "---\nname: greet\n---\nbody",
			wantOK:  false,
		},
		{
			name:    "missing name",
			content: "---\ndescription: Greet.\n---\nbody",
			wantOK:  false,
		},
		{
			name:    "no frontmatter",
			content: "name: greet\ndescription: Greet.\nbody",
			wantOK:  false,
		},
		{
			name:    "unterminated frontmatter",
			content: "---\nname: greet\ndescription: Greet.\nbody",
			wantOK:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// when parsing the frontmatter
			name, desc, body, ok := parseFrontmatter(tc.content)

			// then the result matches expectations
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if name != tc.wantName {
				t.Errorf("name = %q, want %q", name, tc.wantName)
			}
			if desc != tc.wantDesc {
				t.Errorf("description = %q, want %q", desc, tc.wantDesc)
			}
			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

func TestDiscoverFindsValidSkillsAndSkipsInvalid(t *testing.T) {
	// given a project dir with one valid and one invalid skill
	dir := t.TempDir()
	writeSkill(t, dir, "greet", "---\nname: greet\ndescription: Greet someone.\n---\nGreeting instructions.")
	writeSkill(t, dir, "broken", "no frontmatter here")

	// when discovering project skills (no global dir)
	got, err := Discover("", dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// then only the valid skill is returned, with its body
	if len(got) != 1 {
		t.Fatalf("got %d skills, want 1", len(got))
	}
	if got[0].Name != "greet" || got[0].Description != "Greet someone." {
		t.Errorf("unexpected skill metadata: %+v", got[0])
	}
	if got[0].Body != "Greeting instructions." {
		t.Errorf("body = %q, want %q", got[0].Body, "Greeting instructions.")
	}
	if got[0].Dir != filepath.Join(dir, "skills", "greet") {
		t.Errorf("Dir = %q, want skill folder", got[0].Dir)
	}
}

func TestDiscoverProjectOverridesGlobal(t *testing.T) {
	// given a global and a project skill sharing a name, plus a global-only skill
	global := t.TempDir()
	project := t.TempDir()
	writeSkill(t, global, "greet", "---\nname: greet\ndescription: GLOBAL greet.\n---\nglobal body")
	writeSkill(t, global, "calc", "---\nname: calc\ndescription: Global calc.\n---\ncalc body")
	writeSkill(t, project, "greet", "---\nname: greet\ndescription: PROJECT greet.\n---\nproject body")

	// when discovering across both scopes
	got, err := Discover(global, project)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// then the project greet wins and the global-only calc survives
	byName := map[string]Skill{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills, want 2: %+v", len(got), got)
	}
	if byName["greet"].Description != "PROJECT greet." {
		t.Errorf("greet description = %q, want project override", byName["greet"].Description)
	}
	if byName["calc"].Description != "Global calc." {
		t.Errorf("missing global-only calc skill")
	}
}

func TestDiscoverMissingDirsYieldNothing(t *testing.T) {
	// given an empty global dir and a project dir without a skills/ folder
	project := t.TempDir()

	// when discovering
	got, err := Discover("", project)

	// then there is no error and no skills
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d skills, want 0", len(got))
	}
}

func TestCatalog(t *testing.T) {
	// given two skills
	sk := []Skill{
		{Name: "greet", Description: "Greet someone."},
		{Name: "calc", Description: "Do math."},
	}

	// when rendering the catalog
	got := Catalog(sk)

	// then each skill is one line in discovery order
	want := "- greet: Greet someone.\n- calc: Do math."
	if got != want {
		t.Errorf("Catalog = %q, want %q", got, want)
	}

	// and an empty input yields an empty catalog
	if Catalog(nil) != "" {
		t.Errorf("Catalog(nil) = %q, want empty", Catalog(nil))
	}
}
