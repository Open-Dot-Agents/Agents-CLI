package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Open-Dot-Agents/Agents-CLI/internal/config"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "agents:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("a command is required")
	}

	switch args[0] {
	case "init":
		flags := flag.NewFlagSet("init", flag.ContinueOnError)
		flags.SetOutput(stderr)
		root := flags.String("root", ".", "repository root")
		force := flags.Bool("force", false, "replace the generated starter files")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		return config.Init(*root, *force)
	case "validate":
		flags := flag.NewFlagSet("validate", flag.ContinueOnError)
		flags.SetOutput(stderr)
		root := flags.String("root", ".", "repository root")
		format := flags.String("format", "text", "output format: text or json")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if *format != "text" && *format != "json" {
			return fmt.Errorf("unsupported format %q", *format)
		}
		if err := config.Validate(filepath.Join(*root, ".agents")); err != nil {
			if *format == "json" {
				_ = json.NewEncoder(stdout).Encode(map[string]any{
					"schemaVersion": "1.0.0", "standardVersion": "1.0.0",
					"implementation": "reference-cli", "implementationVersion": version,
					"class": "repository", "passed": false,
					"checks": []map[string]any{{"id": "repository.validate", "passed": false, "diagnostic": "ODA-VALIDATE-0001", "message": err.Error()}},
				})
			}
			return err
		}
		if *format == "json" {
			return json.NewEncoder(stdout).Encode(map[string]any{
				"schemaVersion": "1.0.0", "standardVersion": "1.0.0",
				"implementation": "reference-cli", "implementationVersion": version,
				"class": "repository", "passed": true,
				"checks": []map[string]any{{"id": "repository.validate", "passed": true}},
			})
		}
		return nil
	case "capabilities":
		flags := flag.NewFlagSet("capabilities", flag.ContinueOnError)
		flags.SetOutput(stderr)
		vendor := flags.String("vendor", "", "vendor: copilot, codex, or claude")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if *vendor == "" {
			return errors.New("--vendor is required")
		}
		capabilities, err := config.VendorCapabilities(*vendor)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(capabilities)
	case "import":
		flags := flag.NewFlagSet("import", flag.ContinueOnError)
		flags.SetOutput(stderr)
		vendor := flags.String("vendor", "", "source vendor: copilot, codex, or claude")
		root := flags.String("root", ".", "repository root")
		force := flags.Bool("force", false, "replace existing portable files")
		backup := flags.Bool("backup", false, "back up replaced portable files (requires --force)")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if *vendor == "" {
			return errors.New("--vendor is required")
		}
		return config.ImportRepository(*vendor, *root, *force, *backup)
	case "plan", "apply":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		vendor := flags.String("vendor", "", "destination vendor: copilot, codex, or claude")
		root := flags.String("root", ".", "repository root")
		format := flags.String("format", "text", "output format: text or json")
		check := flags.Bool("check", false, "fail if apply would change managed files")
		adopt := flags.Bool("adopt", false, "adopt semantically equivalent unowned entries")
		force := flags.Bool("force", false, "replace conflicting entries")
		backup := flags.Bool("backup", false, "back up changed native files (requires --force)")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if *vendor == "" {
			return errors.New("--vendor is required")
		}
		if *format != "text" && *format != "json" {
			return fmt.Errorf("unsupported format %q", *format)
		}
		options := config.ApplyOptions{Adopt: *adopt, Force: *force, Backup: *backup}
		var result config.PlanResult
		var err error
		if args[0] == "plan" {
			result, err = config.PlanProjection(*vendor, *root, options)
		} else {
			result, err = config.ApplyProjection(*vendor, *root, options)
		}
		if outputErr := writePlan(stdout, result, *format); outputErr != nil {
			return outputErr
		}
		if err != nil {
			return err
		}
		if *check {
			for _, action := range result.Actions {
				if action.Operation != "unchanged" {
					return errors.New("managed configuration changes are required")
				}
			}
		}
		return nil
	case "version":
		flags := flag.NewFlagSet("version", flag.ContinueOnError)
		flags.SetOutput(stderr)
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		_, err := fmt.Fprintf(stdout, "agents %s\n", version)
		return err
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  agents init [--root <directory>] [--force]
  agents validate [--root <directory>] [--format text|json]
  agents capabilities --vendor <copilot|codex|claude>
  agents version
  agents import --vendor <copilot|codex|claude> [--root <directory>] [--force] [--backup]
  agents plan --vendor <copilot|codex|claude> [--root <directory>] [--format text|json] [--check] [--adopt|--force]
  agents apply --vendor <copilot|codex|claude> [--root <directory>] [--format text|json] [--adopt|--force] [--backup]

Root and nested AGENTS.md files are canonical instructions. Portable metadata,
MCP servers, and skills live below .agents. Plan is read-only. Apply performs
structural native-file merges and records generated-entry hashes below
.agents/.state/reference-cli. Conflicts fail unless equivalent content is
adopted or an explicit forced backup is requested.
`)
}

func writePlan(stdout io.Writer, result config.PlanResult, format string) error {
	if format == "json" {
		return json.NewEncoder(stdout).Encode(result)
	}
	if !result.Applicable {
		for _, diagnostic := range result.Diagnostics {
			if _, err := fmt.Fprintln(stdout, diagnostic); err != nil {
				return err
			}
		}
		return nil
	}
	for _, action := range result.Actions {
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", action.Operation, action.Path); err != nil {
			return err
		}
	}
	return nil
}
