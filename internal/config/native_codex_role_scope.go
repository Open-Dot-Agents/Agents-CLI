package config

import "fmt"

// AgentRoleOverrides in Codex 0.154.0 copies these scalars. Other root config
// fields can parse successfully but never become child configuration.
var nativeCodexRoleScalars = map[string]bool{"developer_instructions": true, "model": true, "model_reasoning_effort": true, "model_reasoning_summary": true, "model_verbosity": true, "personality": true, "service_tier": true}

func nativeCodexRoleOverrides(values map[string]any) error {
	ignored := func(path string) error {
		return fmt.Errorf("native agent field %s cannot activate: Codex 0.154.0 ignores this role override; child authority and other settings come from the parent", path)
	}
	for _, key := range nativeSortedKeys(values) {
		if nativeCodexRoleScalars[key] {
			continue
		}
		switch key {
		case "features":
			features, ok := values[key].(map[string]any)
			if !ok {
				return ignored(key)
			}
			allowed := map[string]bool{"shell_tool": true, "apps": true, "personality": true, "plugins": true, "memories": true, "request_permissions_tool": true}
			for _, name := range nativeSortedKeys(features) {
				if !allowed[name] || features[name] != false {
					return ignored(key + "." + name)
				}
			}
		case "skills":
			skills, ok := values[key].(map[string]any)
			if !ok {
				return ignored(key)
			}
			for _, name := range nativeSortedKeys(skills) {
				value := skills[name]
				switch name {
				case "include_instructions":
					if value != false {
						return ignored(key + "." + name)
					}
				case "bundled":
					bundled, ok := value.(map[string]any)
					if !ok || bundled["enabled"] != false {
						return ignored(key + "." + name)
					}
				case "config":
					entries, ok := value.([]any)
					if !ok {
						return ignored(key + "." + name)
					}
					for i, raw := range entries {
						entry, ok := raw.(map[string]any)
						if !ok || entry["enabled"] != false {
							return ignored(fmt.Sprintf("skills.config[%d]", i))
						}
					}
				default:
					return ignored(key + "." + name)
				}
			}
		default:
			return ignored(key)
		}
	}
	return nil
}

func nativeCodexRoleFeature(feature NativeFeature) NativeFeature {
	feature.Limitation = "Codex 0.154.0 roles apply a bounded set of model, instruction, feature-reduction, and skill-reduction overrides. Other config roots and capability increases remain inactive as a whole artifact. Local child requests verify model and reasoning selection with inherited provider, shell-tool reduction, and skill instruction and selector reductions. Other reductions and scalar choices require separate behavior evidence. Parent authority remains external."
	feature.NativeStatus = "bounded-child-configuration"
	feature.Evidence = []string{"WORKBENCH/evidence/native-draft2-debug/codex-agent-role-scope.sources.json"}
	for _, name := range []string{"provider", "shell", "skills", "selector"} {
		feature.Evidence = append(feature.Evidence, "WORKBENCH/evidence/native-draft2-debug/codex-role-"+name+"-"+feature.Scope+"-checked.json")
	}
	return feature
}
