package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"klauscode/internal/tools"
	"klauscode/internal/tools/calculate"
)

// write places contents at dir/name, failing the test on error.
func write(t *testing.T, dir, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestFirstInstructionFile(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string // file name -> contents
		want  string
	}{
		{name: "agents.md present", files: map[string]string{"AGENTS.md": "agents"}, want: "agents"},
		{name: "claude.md fallback", files: map[string]string{"CLAUDE.md": "claude"}, want: "claude"},
		{name: "agents.md wins over claude.md", files: map[string]string{"AGENTS.md": "agents", "CLAUDE.md": "claude"}, want: "agents"},
		{name: "neither present", files: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given a directory with the listed files
			dir := t.TempDir()
			for name, contents := range tt.files {
				write(t, dir, name, contents)
			}

			// when reading the first instruction file
			got, err := firstInstructionFile(dir)

			// then the expected contents come back with no error
			if err != nil {
				t.Fatalf("firstInstructionFile returned error: %v", err)
			}
			if got != tt.want {
				t.Errorf("firstInstructionFile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstInstructionFileEmptyDir(t *testing.T) {
	// given an empty dir argument (home lookup failed)
	// when reading
	got, err := firstInstructionFile("")

	// then it yields no content and no error
	if err != nil {
		t.Fatalf("firstInstructionFile returned error: %v", err)
	}
	if got != "" {
		t.Errorf("firstInstructionFile = %q, want empty", got)
	}
}

func TestLoadInstructions(t *testing.T) {
	// given a global dir and a project dir each with an instruction file
	globalDir := t.TempDir()
	projectDir := t.TempDir()
	write(t, globalDir, "CLAUDE.md", "be concise")
	write(t, projectDir, "AGENTS.md", "run go test")

	// when combining them
	got, err := LoadInstructions(globalDir, projectDir)
	if err != nil {
		t.Fatalf("LoadInstructions returned error: %v", err)
	}

	// then both blocks appear, global before project
	if !strings.Contains(got, "be concise") {
		t.Errorf("missing global content in %q", got)
	}
	if !strings.Contains(got, "run go test") {
		t.Errorf("missing project content in %q", got)
	}
	if !strings.Contains(got, "[Global instructions") || !strings.Contains(got, "[Project instructions") {
		t.Errorf("missing labeled headers in %q", got)
	}
	if strings.Index(got, "be concise") > strings.Index(got, "run go test") {
		t.Errorf("global should precede project, got %q", got)
	}
}

func TestLoadInstructionsGlobalOnly(t *testing.T) {
	// given only a global file (project dir empty)
	globalDir := t.TempDir()
	write(t, globalDir, "AGENTS.md", "global only")

	// when combining
	got, err := LoadInstructions(globalDir, t.TempDir())
	if err != nil {
		t.Fatalf("LoadInstructions returned error: %v", err)
	}

	// then only the global block appears
	if !strings.Contains(got, "global only") || !strings.Contains(got, "[Global instructions") {
		t.Errorf("missing global block in %q", got)
	}
	if strings.Contains(got, "[Project instructions") {
		t.Errorf("unexpected project block in %q", got)
	}
}

func TestLoadInstructionsNeither(t *testing.T) {
	// given two empty dirs
	// when combining
	got, err := LoadInstructions(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("LoadInstructions returned error: %v", err)
	}

	// then the result is empty
	if got != "" {
		t.Errorf("LoadInstructions = %q, want empty", got)
	}
}

func TestBuildSystemPromptWithInstructions(t *testing.T) {
	// given a registry and some instructions
	reg := tools.NewRegistry()
	reg.Register(calculate.New())

	// when building the prompt with instructions
	prompt := BuildSystemPrompt(reg, "", "always answer in French")

	// then the instructions section and the footer are both present
	if !strings.Contains(prompt, "USER & PROJECT INSTRUCTIONS") {
		t.Errorf("missing instructions marker in prompt")
	}
	if !strings.Contains(prompt, "always answer in French") {
		t.Errorf("missing instruction content in prompt")
	}
	if !strings.Contains(prompt, "Let's begin.") {
		t.Errorf("footer dropped from prompt")
	}
}

func TestBuildSystemPromptWithoutInstructions(t *testing.T) {
	// given a registry and blank instructions
	reg := tools.NewRegistry()
	reg.Register(calculate.New())

	// when building the prompt with only whitespace
	prompt := BuildSystemPrompt(reg, "", "   \n  ")

	// then no instructions section is emitted
	if strings.Contains(prompt, "USER & PROJECT INSTRUCTIONS") {
		t.Errorf("unexpected instructions marker for blank instructions")
	}
}

func TestBuildSystemPromptWithSkills(t *testing.T) {
	// given a registry and an Agent Skills catalog
	reg := tools.NewRegistry()
	reg.Register(calculate.New())
	catalog := "- greet: Greet a person enthusiastically by name."

	// when building the prompt with the catalog and some instructions
	prompt := BuildSystemPrompt(reg, catalog, "always answer in French")

	// then the skills section, its content, and the footer are all present
	if !strings.Contains(prompt, "AGENT SKILLS") {
		t.Errorf("missing AGENT SKILLS marker in prompt")
	}
	if !strings.Contains(prompt, catalog) {
		t.Errorf("missing skill catalog content in prompt")
	}
	if !strings.Contains(prompt, "Let's begin.") {
		t.Errorf("footer dropped from prompt")
	}

	// and the skills section precedes the instructions section, which precedes the footer
	skillsAt := strings.Index(prompt, "AGENT SKILLS")
	instrAt := strings.Index(prompt, "USER & PROJECT INSTRUCTIONS")
	footerAt := strings.Index(prompt, "Let's begin.")
	if !(skillsAt < instrAt && instrAt < footerAt) {
		t.Errorf("section order wrong: skills=%d instructions=%d footer=%d", skillsAt, instrAt, footerAt)
	}
}

func TestBuildSystemPromptWithoutSkills(t *testing.T) {
	// given a registry and a blank catalog
	reg := tools.NewRegistry()
	reg.Register(calculate.New())

	// when building the prompt with no skills
	prompt := BuildSystemPrompt(reg, "", "")

	// then no skills section is emitted
	if strings.Contains(prompt, "AGENT SKILLS") {
		t.Errorf("unexpected AGENT SKILLS marker for empty catalog")
	}
}
