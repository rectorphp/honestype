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
  docblockcheck instrument <path>          Inject checks into .php files in place,
                                           write docblock_check.php next to them.
  docblockcheck report [-checkstyle out.xml] [-github] <log>
                                           Render the collected log as a table
                                           with source snippets; optionally emit
                                           a Checkstyle XML report and/or GitHub
                                           Actions annotations.

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
		changed, err := instrumentTree(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("instrumented %d file(s); wrote docblock_check.php\n", changed)

	case "report":
		fs := flag.NewFlagSet("report", flag.ExitOnError)
		checkstyle := fs.String("checkstyle", "", "write a Checkstyle XML report to this path")
		github := fs.Bool("github", false, "emit GitHub Actions ::warning annotations")
		_ = fs.Parse(os.Args[2:])
		if fs.NArg() < 1 {
			usage()
			os.Exit(2)
		}
		found, err := renderReport(fs.Arg(0), *checkstyle, *github)
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
