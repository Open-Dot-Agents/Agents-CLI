package config

import "path/filepath"

// Inheritance is native discovery of an explicitly configured parent tree.
// It is not permission for a child import to read, copy, or own parent assets.
func nativeCopilotInheritedSkillFeature() NativeFeature {
	return NativeFeature{
		Feature: "artifact:skill-discovery:/Parent .github/skills/", Scope: "project",
		Source: "<selected-parent-root>/.github/skills/", Destination: "<selected-parent-root>/.agents/skills/",
		Disposition: "portable-mapping", NativeStatus: "bounded-inherited-skill-execution",
		Ownership:  "canonical package at selected parent; no ownership by descendant projection",
		Authority:  "explicit parent root selection; native repository and trust boundaries; no trust grant",
		Activation: "import and apply at the owning parent root; start a descendant native session",
		Evidence:   []string{"docs/COPILOT_PARENT_SKILLS.md", "WORKBENCH/conformance/verify_copilot_parent_skills.py"},
		Limitation: "Copilot inherits parent .github/skills, .agents/skills, and .claude/skills up to the nearest Git root. A nested Git repository stops inheritance. Local definitions can shadow inherited names. Child import never captures parent assets; configure the parent through its explicit --root. Packages remain complete at their owning root.",
	}
}

func nativePlanCopilotInheritedSkills(plan *PlanResult, root string, selected bool) {
	feature := nativeCopilotInheritedSkillFeature()
	if selected {
		feature.Source = filepath.Join(root, "skills")
		feature.Destination = feature.Source
		feature.Activation = "native discovery in this root and descendant sessions inside its repository boundary"
		plan.Native.Features = append(plan.Native.Features, feature)
	}
	external := feature
	external.Feature = "skills:external-inherited-discovery"
	external.Source = "ancestor .github/skills, .agents/skills, and .claude/skills inside the native discovery boundary"
	external.Destination = "native inherited skill catalogue"
	external.Disposition = "external"
	external.Activation = "external candidates; this plan does not enumerate or activate ancestor packages"
	external.Ownership = "external parent source; not owned by this projection"
	external.Authority = "native discovery and existing trust; no ancestor read or write by this adapter"
	plan.Native.Features = append(plan.Native.Features, external)
	plan.Native.RequiredActions = append(plan.Native.RequiredActions, "Inspect inherited and shadowed skill sources with copilot skill list --json from the intended native working directory. Configure an owning parent through explicit --root; a child import does not copy ancestor packages.")
}
