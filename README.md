# ErrorTransformer

A Go tool that automatically rewrites bare `return err` statements across a Go
codebase into structured `fmt.Errorf` calls with `%w` error wrapping.

Every transformed error message embeds a precise call-site tag so that error
strings are self-documenting and `errors.Is` / `errors.As` unwrapping chains
work correctly.

## Generated format

```
[lib.pkg.ReceiverType.FuncName] callee.Call error: %w   ← method on a type
[lib.pkg.FuncName] callee.Call error: %w                ← plain function
[lib.pkg.FuncName] error: %w                            ← callee not found nearby
```

### Before / after example

```go
package provider

// Before
func (z *MyProvider) GetRecords(param string) error {
    records, err := mod.GetRecords(param)
    if err != nil {
        return err
    }
    ...
}

// After
func (z *MyProvider) GetRecords(param string) error {
    records, err := mod.GetRecords(param)
    if err != nil {
        return fmt.Errorf("[golib.provider.MyProvider.GetRecords] mod.GetRecords error: %w", err)
    }
    ...
}
```

The location tag has four parts:

| Part | Example | Source |
|---|---|---|
| Library prefix | `golib` | Last segment of `module` in `go.mod` |
| Package | `provider` | `package` declaration in the file |
| Receiver type | `MyProvider` | Receiver in `func (z *MyProvider) ...` |
| Function name | `GetRecords` | Function name |

Plain functions omit the receiver: `[golib.doi.Publish]`.

## What is transformed — and what is not

**Transformed** — bare error variable returns at the end of a return statement:

```go
return err
return nil, err
return "", "", dberr
```

**Left unchanged:**

| Pattern | Reason |
|---|---|
| `return fmt.Errorf(...)` | Already wrapped |
| `return errors.New(...)` | Already a new error |
| `return err.Error()` | Returns a string, not an error |
| `func Foo() error { return Bar() }` | Thin delegation wrapper — wrapping would hide the real call site |
| `*_test.go` files | Test files are skipped entirely |
| `vendor/` directory | Third-party code is never touched |


## Building the CLI tool

**Requirements:** Go 1.21 or later. No external dependencies — standard library only.

```bash
git clone https://github.com/vkuznet/errortransformer
cd errortransformer

# Build and install to $GOPATH/bin
go install ./cmd/errortransformer

# Or build a local binary
go build -o errortransformer ./cmd/errortransformer
```

Verify the build:

```bash
./errortransformer -h
```

---

## CLI usage

```
errortransformer [flags] <path>
```

`<path>` is the root directory of the Go module or package to scan.
Defaults to `.` (current directory) when omitted.

### Flags

| Flag | Default | Description |
|---|---|---|
| `-prefix` | *(auto)* | Library prefix embedded in every error message. Auto-detected from the last segment of the `module` declaration in `go.mod`. Pass `-prefix ""` to suppress the prefix entirely. |
| `-out` | `errors.patch` | File name of the unified diff patch to write. |
| `-v` | false | Verbose: print every file that was modified. |

### Examples

```bash
# Auto-detect prefix from go.mod, write errors.patch in the current directory
errortransformer .

# Scan a specific module path
errortransformer /path/to/mymodule

# Override the library prefix
errortransformer -prefix mylib /path/to/mymodule

# Write the patch to a custom file name
errortransformer -out wrap_errors.patch ./services

# Verbose mode — see every file that changes
errortransformer -v .
```

### Typical session output

```
detected library prefix : golib
scanning                : /home/user/golib

50 file(s) changed, 209 return statement(s) transformed.
patch written to        : errors.patch

next steps:
  1. Review  : less errors.patch
  2. Apply   : patch -p1 < errors.patch
  3. Format  : gofmt -w .
               # or: find . -name "*.go" -not -path "./vendor/*" | xargs goimports -w
```

---

## Applying the patch

The tool never modifies files directly. It writes a standard unified diff that
you inspect before applying.

```bash
# 1. Generate the patch
errortransformer -v .

# 2. Review it (important — always read the diff before applying)
less errors.patch

# 3. Apply from the repository root
patch -p1 < errors.patch

# 4. Re-sort the newly added "fmt" imports
gofmt -w .

# If you use goimports it handles import grouping and deduplication too
find . -name "*.go" -not -path "./vendor/*" | xargs goimports -w

# 5. Verify nothing is broken
go build ./...
go test ./...
```

To undo an applied patch:

```bash
patch -p1 -R < errors.patch
```

---

## Using the library

Import the package to drive transformations programmatically — useful for
custom tooling, CI pipelines, or editor integrations.

```bash
go get github.com/vkuznet/errortransformer
```

### Exported API

```go
// FileResult is returned for every file that was changed or had a processing error.
type FileResult struct {
    Path    string // absolute path to the source file
    Changed bool   // true when at least one return was transformed
    Patch   string // unified diff text; empty when Changed is false
    Err     error  // non-nil when the file could not be read or parsed
}

// TransformDir walks root recursively and transforms every non-test .go file.
// Pass libPrefix="" to auto-detect from go.mod.
func TransformDir(root, libPrefix string) []FileResult

// TransformFile transforms a single file.
// root is used only to compute the relative path in the diff header.
func TransformFile(path, root, libPrefix string) FileResult

// LibPrefixFromGoMod walks up from dir to find go.mod and returns the last
// segment of the module path, e.g. "golib" for "github.com/acme/golib".
// Returns "" when no go.mod is found.
func LibPrefixFromGoMod(dir string) string
```

### Example: transform a directory and print the patch to stdout

```go
package main

import (
    "fmt"
    "os"

    et "github.com/vkuznet/errortransformer"
)

func main() {
    root := "./mymodule"

    results := et.TransformDir(root, "") // "" = auto-detect prefix
    for _, r := range results {
        if r.Err != nil {
            fmt.Fprintf(os.Stderr, "error processing %s: %v\n", r.Path, r.Err)
            continue
        }
        if r.Changed {
            fmt.Print(r.Patch)
        }
    }
}
```

### Example: transform a single file and write the patch

```go
package main

import (
    "fmt"
    "os"

    et "github.com/vkuznet/errortransformer"
)

func main() {
    root   := "/path/to/mymodule"
    target := "/path/to/mymodule/services/api.go"

    prefix := et.LibPrefixFromGoMod(root)
    result := et.TransformFile(target, root, prefix)

    if result.Err != nil {
        fmt.Fprintf(os.Stderr, "failed: %v\n", result.Err)
        os.Exit(1)
    }
    if !result.Changed {
        fmt.Println("no changes")
        return
    }

    if err := os.WriteFile("api.patch", []byte(result.Patch), 0644); err != nil {
        fmt.Fprintf(os.Stderr, "cannot write patch: %v\n", err)
        os.Exit(1)
    }
    fmt.Println("patch written to api.patch")
}
```

### Example: inspect the prefix before running

```go
prefix := et.LibPrefixFromGoMod("/path/to/mymodule")
if prefix == "" {
    fmt.Println("no go.mod found, running without prefix")
}

results := et.TransformDir("/path/to/mymodule", prefix)
```

### Example: collect only changed paths

```go
results := et.TransformDir(".", "")

var changed []string
for _, r := range results {
    if r.Changed {
        changed = append(changed, r.Path)
    }
}
fmt.Printf("%d file(s) would be modified\n", len(changed))
```

---

## How the location tag is built

Given this function signature and `go.mod`:

```
module github.com/vkuznet/golib  →  prefix = "golib"
package provider
func (z *MyProvider) GetRecords(...) error
```

The tag is assembled as:

```
[golib  . provider .  MyProvider  .  GetRecords]
  ↑          ↑            ↑              ↑
prefix    package    receiver type   func name
```

For a plain function (`func ParseConfig(...)`):

```
[golib.config.ParseConfig]
```

The callee is inferred by scanning up to 15 lines backwards from the `return`
statement, looking for the assignment that produced the error variable:

```go
config, err := viper.Unmarshal(&cfg)   // ← "viper.Unmarshal" captured here
if err != nil {
    return config, err                 // ← transformed line
}
```

Result:

```go
return config, fmt.Errorf("[golib.config.ParseConfig] viper.Unmarshal error: %w", err)
```

---

## License

MIT
