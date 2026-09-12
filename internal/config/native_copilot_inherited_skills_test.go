package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCopilotParentSkillMappingPreservesOwningRoot(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "packages/child")
	writeFixture(t, filepath.Join(parent, ".github/skills/fixture/SKILL.md"), importedSkill)
	writeFixture(t, filepath.Join(parent, ".github/skills/fixture/data.bin"), "\x00\xff")
	writeFixture(t, filepath.Join(child, ".github/copilot-instructions.md"), "Child instructions.\n")
	if err := ImportRepositoryWithOptions("copilot", parent, WriteOptions{Experimental: true}); err != nil {
		t.Fatal(err)
	}
	plan, err := ApplyProjection("copilot", parent, ApplyOptions{Experimental: true})
	if err != nil {
		t.Fatal(err)
	}
	mapped := false
	for _, feature := range plan.Native.Features {
		if feature.Feature == "artifact:skill-discovery:/Parent .github/skills/" {
			mapped = feature.Source == filepath.Join(parent, ".agents/skills") && feature.Destination == feature.Source && feature.Scope == "project"
		}
	}
	if !mapped {
		t.Fatal("parent portable discovery mapping absent")
	}
	before := nativeSkillTestSnapshot(t, filepath.Join(parent, ".agents"))
	nativeBefore := nativeSkillTestSnapshot(t, filepath.Join(parent, ".github/skills"))
	if err := ImportRepositoryWithOptions("copilot", child, WriteOptions{Experimental: true, Force: true}); err != nil {
		t.Fatal(err)
	}
	plan, err = ApplyProjection("copilot", child, ApplyOptions{Experimental: true, Adopt: true, Force: true, Backup: true})
	if err != nil {
		t.Fatal(err)
	}
	external := false
	for _, feature := range plan.Native.Features {
		if feature.Feature == "skills:external-inherited-discovery" {
			external = feature.Disposition == "external"
		}
	}
	if !external {
		t.Fatal("child plan omitted its external native discovery boundary")
	}
	if _, err := os.Stat(filepath.Join(child, ".agents/skills/fixture")); !os.IsNotExist(err) {
		t.Fatal("child captured parent skill", err)
	}
	if !reflect.DeepEqual(before, nativeSkillTestSnapshot(t, filepath.Join(parent, ".agents"))) || !reflect.DeepEqual(nativeBefore, nativeSkillTestSnapshot(t, filepath.Join(parent, ".github/skills"))) {
		t.Fatal("child operation changed parent assets")
	}
}
