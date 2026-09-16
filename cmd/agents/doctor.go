package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Open-Dot-Agents/Agents-CLI/internal/config"
)

func runDoctor(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	experimental := flags.Bool("experimental", false, "inspect the experimental development preset")
	vendor := flags.String("vendor", "", "native vendor: codex or copilot")
	root := flags.String("root", ".", "project root")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if !*experimental || (*vendor != "codex" && *vendor != "copilot") {
		return errors.New("doctor requires --experimental and --vendor codex or copilot")
	}
	if flags.NArg() != 0 || (*format != "text" && *format != "json") {
		return errors.New("doctor accepts no positional arguments and requires --format text or json")
	}
	result, err := config.DoctorDevelopment(*vendor, *root)
	if err != nil {
		return err
	}
	if err := writeDoctor(stdout, result, *format); err != nil {
		return err
	}
	if !result.Ready {
		return errors.New("development setup needs attention; see doctor findings")
	}
	return nil
}

func doctorShellArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\r\n'\"`$&;|<>(){}[]*?!\\") {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeDoctor(w io.Writer, result config.DoctorResult, format string) error {
	if format == "json" {
		return json.NewEncoder(w).Encode(result)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Development configuration: %s (vendor: %s; scope: %s)\n", result.ConfigurationState, result.Vendor, result.Scope)
	if p := result.RequestedPolicy; p != nil {
		fmt.Fprintf(&output, "Requested: project work %s; local commits %s; external changes %s; destructive work %s.\n", p.ProjectWork, p.LocalCommits, p.ExternalChanges, p.DestructiveWork)
	}
	for _, check := range doctorTextChecks(result.Checks) {
		fmt.Fprintf(&output, "%s\t%s\t%s\n", check.Status, check.ID, check.Message)
		for _, path := range check.Paths {
			fmt.Fprintf(&output, "  Path: %s\n", strconv.Quote(path))
		}
		for _, command := range check.Commands {
			parts := make([]string, len(command))
			for i, arg := range command {
				parts[i] = doctorShellArgument(arg)
			}
			fmt.Fprintf(&output, "  Next: %s\n", strings.Join(parts, " "))
		}
		if check.NextStep != "" {
			fmt.Fprintf(&output, "  Next: %s\n", check.NextStep)
		}
	}
	_, err := io.WriteString(w, output.String())
	return err
}

// A submodule shares the same diagnosis as its parent when both metadata
// directories have the same mount restriction. Print the explanation once.
func doctorTextChecks(checks []config.DoctorCheck) []config.DoctorCheck {
	var result []config.DoctorCheck
	groups := map[string]int{}
	for _, check := range checks {
		if check.ID == "filesystem.git" {
			key := check.Status + ":" + check.Message
			if index, ok := groups[key]; ok {
				result[index].Paths = append(result[index].Paths, check.Paths...)
				continue
			}
			groups[key] = len(result)
		}
		check.Paths = append([]string(nil), check.Paths...)
		result = append(result, check)
	}
	return result
}
