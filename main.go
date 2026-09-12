package main

import (
	_ "embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed runtime/docblock_check.php
var helperPHP []byte

func writeHelper(dir string) error {
	dest := filepath.Join(dir, "docblock_check.php")
	return os.WriteFile(dest, helperPHP, 0o644)
}

func usage() {
	fmt.Fprint(os.Stderr, `docblockcheck - spot invalid @param/@return array types at runtime

Usage:
  docblockcheck instrument <path>...       Inject checks into .php files in place,
                                           write docblock_check.php next to them.
                                           Accepts multiple paths.
  docblockcheck report [-checkstyle out.xml] [-github] [-skip 'Class::method()'] [-skip-array-keys] <log>
                                           Render the collected log as a table
                                           with source snippets; optionally emit
                                           a Checkstyle XML report and/or GitHub
                                           Actions annotations. Repeat -skip to
                                           drop known false-positive methods.
                                           Pass -skip-array-keys to drop all
                                           array-key type mismatches.

Workflow:
  1. docblockcheck instrument src/
  2. include the generated docblock_check.php from your PHPUnit bootstrap
  3. run the unit tests (writes ./docblock-check.log, or $DOCBLOCK_CHECK_LOG)
  4. docblockcheck report -checkstyle checkstyle.xml docblock-check.log
  5. restore the source (git checkout) once the log is collected
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "instrument":
		if len(os.Args) < 3 {
			usage()
			os.Exit(2)
		}
		changed, err := instrumentTree(os.Args[2:]...)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("instrumented %d file(s); wrote docblock_check.php\n", changed)

	case "report":
		fs := flag.NewFlagSet("report", flag.ExitOnError)
		checkstyle := fs.String("checkstyle", "", "write a Checkstyle XML report to this path")
		github := fs.Bool("github", false, "emit GitHub Actions ::warning annotations")
		skipArrayKeys := fs.Bool("skip-array-keys", false, "drop array-key type mismatches (e.g. array<string, X> keyed by int)")
		var skips skipList
		fs.Var(&skips, "skip", "skip a method's findings as a false positive, e.g. -skip 'Class::method()' (repeatable)")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			usage()
			os.Exit(2)
		}
		found, err := renderReport(fs.Arg(0), *checkstyle, *github, *skipArrayKeys, skips)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if found > 0 {
			os.Exit(1) // fail CI when any invalid docblock type is present
		}

	default:
		usage()
		os.Exit(2)
	}
}
