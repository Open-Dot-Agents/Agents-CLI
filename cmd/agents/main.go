package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Open-Dot-Agents/Agents-CLI/internal/config"
)

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
		target := flags.String("target", ".", "directory in which to create .agents")
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
		return config.Init(*target, *force)
	case "import", "export":
		flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		vendor := flags.String("vendor", "", "source or destination vendor: copilot or codex")
		sourceDefault := "."
		targetDefault := ".agents"
		if args[0] == "export" {
			sourceDefault = ".agents"
			targetDefault = "."
		}
		source := flags.String("source", sourceDefault, "source directory")
		target := flags.String("target", targetDefault, "target directory")
		force := flags.Bool("force", false, "overwrite generated files and skills")
		backup := flags.Bool("backup", false, "back up existing destination directories before overwriting (requires --force)")
		diff := flags.Bool("diff", false, "show a summary of managed configuration changes")
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
		options := config.WriteOptions{Force: *force, Backup: *backup}
		if args[0] == "import" {
			return runWithDiff(*diff, stdout, []string{*target}, func() error {
				return config.ImportWithOptions(*vendor, *source, *target, options)
			})
		}
		paths, err := vendorTargetPaths(*vendor, *target)
		if err != nil {
			return err
		}
		return runWithDiff(*diff, stdout, paths, func() error {
			return config.ExportWithOptions(*vendor, *source, *target, options)
		})
	case "convert":
		flags := flag.NewFlagSet("convert", flag.ContinueOnError)
		flags.SetOutput(stderr)
		from := flags.String("from", "", "source vendor: copilot or codex")
		to := flags.String("to", "", "destination vendor: copilot or codex")
		source := flags.String("source", ".", "source directory")
		target := flags.String("target", ".", "target directory")
		force := flags.Bool("force", false, "overwrite generated files and skills")
		backup := flags.Bool("backup", false, "back up existing destination directories before overwriting (requires --force)")
		diff := flags.Bool("diff", false, "show a summary of managed configuration changes")
		if err := flags.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected argument %q", flags.Arg(0))
		}
		if *from == "" || *to == "" {
			return errors.New("--from and --to are required")
		}
		paths, err := vendorTargetPaths(*to, *target)
		if err != nil {
			return err
		}
		return runWithDiff(*diff, stdout, paths, func() error {
			return config.ConvertWithOptions(*from, *to, *source, *target, config.WriteOptions{
				Force:  *force,
				Backup: *backup,
			})
		})
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
  agents init [--target <directory>] [--force]
  agents import --vendor <copilot|codex> [--source <directory>] [--target <.agents directory>] [--force] [--backup] [--diff]
  agents export --vendor <copilot|codex> [--source <.agents directory>] [--target <directory>] [--force] [--backup] [--diff]
  agents convert --from <copilot|codex> --to <copilot|codex> [--source <directory>] [--target <directory>] [--force] [--backup] [--diff]

The init command creates a canonical .agents starter tree. Other commands translate
MCP server configuration and copy skills. Canonical configuration uses
.agents/tools/mcp.json and .agents/skills. Copilot and Codex skills are stored at
.agents/skills; their MCP configuration is .github/mcp.json and .codex/config.toml.
Import defaults to source "." and target ".agents"; export defaults to source
".agents" and target ".". Convert defaults to "." for both directories.
`)
}

func vendorTargetPaths(vendor, target string) ([]string, error) {
	switch strings.ToLower(vendor) {
	case "copilot":
		return []string{filepath.Join(target, ".github"), filepath.Join(target, ".agents")}, nil
	case "codex":
		return []string{filepath.Join(target, ".codex"), filepath.Join(target, ".agents")}, nil
	default:
		return nil, fmt.Errorf("unsupported vendor %q (supported: copilot, codex)", vendor)
	}
}

func runWithDiff(enabled bool, stdout io.Writer, paths []string, operation func() error) error {
	if !enabled {
		return operation()
	}
	before, err := config.CaptureFiles(paths...)
	if err != nil {
		return err
	}
	if err := operation(); err != nil {
		return err
	}
	after, err := config.CaptureFiles(paths...)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(stdout, config.RenderChangeSummary(before, after))
	return err
}
