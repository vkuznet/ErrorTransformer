// ErrorTransformer — a CLI tool that rewrites bare "return err" statements in
// Go source files to use fmt.Errorf with structured location prefixes and %w
// error wrapping, then emits a unified diff patch you can inspect and apply.
//
// Usage:
//
//	errortransformer [flags] <path>
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	et "github.com/vkuznet/errortransformer"
)

const usageText = `ErrorTransformer — wraps bare Go error returns with fmt.Errorf and structured location prefixes.

USAGE
  errortransformer [flags] <path>

  <path>  Root directory of the Go module or package to transform.
          Defaults to the current directory (".").

FLAGS
  -prefix string
        Library prefix inserted into every error message, e.g. "golib".
        When omitted the tool reads go.mod and uses the last segment of the
        module path  (e.g. "github.com/acme/golib" -> "golib").
        Pass -prefix="" to suppress the prefix entirely.

  -out string
        Name of the unified diff patch file to write. (default "errors.patch")

  -v    Verbose output: print the path of every modified file.

OUTPUT FORMAT
  Each transformed return statement becomes one of:

    // method on a named receiver type:
    return fmt.Errorf("[lib.pkg.ReceiverType.FuncName] callee.Call error: %%w", err)

    // plain function with an identifiable callee:
    return fmt.Errorf("[lib.pkg.FuncName] callee.Call error: %%w", err)

    // plain function, no nearby callee assignment found:
    return fmt.Errorf("[lib.pkg.FuncName] error: %%w", err)

  Lines that already contain fmt.Errorf or errors.* are left unchanged.
  Single-delegation wrappers (func Foo() error { return Bar() }) are skipped.

APPLYING THE PATCH
  Review the patch first, then apply it from the repository root:

    patch -p1 < errors.patch

  After applying, re-format the changed files so the new "fmt" import is
  sorted correctly alongside the other imports:

    gofmt -w ./...

  If you use goimports it will also clean up any duplicate imports:

    goimports -w ./...

EXAMPLES
  # Transform the current directory, auto-detect prefix from go.mod
  errortransformer .

  # Transform a specific module with an explicit prefix
  errortransformer -prefix mylib /path/to/mymodule

  # Write the patch under a custom name
  errortransformer -out wrap_errors.patch ./services

  # Inspect before applying
  errortransformer -v . && less errors.patch && patch -p1 < errors.patch
`

func main() {
	var (
		prefix  = flag.String("prefix", "", "Library prefix for error messages (auto-detected from go.mod if empty)")
		outFile = flag.String("out", "errors.patch", "Output patch file name")
		verbose = flag.Bool("v", false, "Verbose: print every changed file")
	)

	flag.Usage = func() { fmt.Fprint(os.Stderr, usageText) }
	flag.Parse()

	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fatalf("cannot resolve path %q: %v", root, err)
	}
	if _, err := os.Stat(absRoot); os.IsNotExist(err) {
		fatalf("path does not exist: %s", absRoot)
	}

	// Determine effective library prefix
	effectivePrefix := *prefix
	if effectivePrefix == "" {
		effectivePrefix = et.LibPrefixFromGoMod(absRoot)
		if effectivePrefix != "" {
			fmt.Printf("detected library prefix : %s\n", effectivePrefix)
		} else {
			fmt.Println("no go.mod found — proceeding without library prefix")
		}
	} else {
		fmt.Printf("using library prefix    : %s\n", effectivePrefix)
	}

	fmt.Printf("scanning                : %s\n\n", absRoot)

	results := et.TransformDir(absRoot, effectivePrefix)

	// Partition into changed files and errors
	var changed []et.FileResult
	var errs []et.FileResult
	for _, r := range results {
		switch {
		case r.Err != nil:
			errs = append(errs, r)
		case r.Changed:
			changed = append(changed, r)
		}
	}

	// Deterministic output order
	sort.Slice(changed, func(i, j int) bool { return changed[i].Path < changed[j].Path })

	if len(errs) > 0 {
		fmt.Fprintln(os.Stderr, "errors during processing:")
		for _, r := range errs {
			fmt.Fprintf(os.Stderr, "  %s: %v\n", r.Path, r.Err)
		}
		fmt.Fprintln(os.Stderr)
	}

	if len(changed) == 0 {
		fmt.Println("no changes — nothing to patch.")
		return
	}

	if *verbose {
		for _, r := range changed {
			rel, _ := filepath.Rel(absRoot, r.Path)
			fmt.Printf("  modified: %s\n", rel)
		}
		fmt.Println()
	}

	// Write patch file
	f, err := os.Create(*outFile)
	if err != nil {
		fatalf("cannot create patch file %q: %v", *outFile, err)
	}
	defer f.Close()

	totalTransforms := 0
	for _, r := range changed {
		if _, err := f.WriteString(r.Patch); err != nil {
			fatalf("cannot write to patch file: %v", err)
		}
		// Count transformed lines: "+" lines (not "+++") that contain fmt.Errorf
		for _, line := range strings.Split(r.Patch, "\n") {
			if len(line) >= 2 && line[0] == '+' && line[1] != '+' &&
				strings.Contains(line, "fmt.Errorf") {
				totalTransforms++
			}
		}
	}

	fmt.Printf("%d file(s) changed, %d return statement(s) transformed.\n",
		len(changed), totalTransforms)
	fmt.Printf("patch written to        : %s\n\n", *outFile)
	fmt.Println("next steps:")
	fmt.Printf("  1. Review  : less %s\n", *outFile)
	fmt.Printf("  2. Apply   : patch -p1 < %s\n", *outFile)
	fmt.Println("  3. Format  : gofmt -w ./...")
	fmt.Println("               # or: goimports -w ./...")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "errortransformer: "+format+"\n", args...)
	os.Exit(1)
}
