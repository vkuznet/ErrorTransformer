package errortransformer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// writeFile creates a file at path inside dir with the given content.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", full, err)
	}
	return full
}

// assertContains fails the test if sub is not found in s.
func assertContains(t *testing.T, s, sub, label string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s: expected to contain %q\ngot:\n%s", label, sub, s)
	}
}

// assertNotContains fails the test if sub is found in s.
func assertNotContains(t *testing.T, s, sub, label string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%s: expected NOT to contain %q\ngot:\n%s", label, sub, s)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// LibPrefixFromGoMod
// ─────────────────────────────────────────────────────────────────────────────

func TestLibPrefixFromGoMod_Standard(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module github.com/CHESSComputing/golib\n\ngo 1.21\n")
	got := LibPrefixFromGoMod(dir)
	if got != "golib" {
		t.Errorf("expected %q, got %q", "golib", got)
	}
}

func TestLibPrefixFromGoMod_ShortPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module myapp\n\ngo 1.21\n")
	got := LibPrefixFromGoMod(dir)
	if got != "myapp" {
		t.Errorf("expected %q, got %q", "myapp", got)
	}
}

func TestLibPrefixFromGoMod_TrailingSlash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module github.com/acme/services/\n\ngo 1.21\n")
	got := LibPrefixFromGoMod(dir)
	if got != "services" {
		t.Errorf("expected %q, got %q", "services", got)
	}
}

func TestLibPrefixFromGoMod_WalksUp(t *testing.T) {
	dir := t.TempDir()
	// go.mod is in the parent; we pass a subdirectory
	writeFile(t, dir, "go.mod", "module github.com/acme/mylib\n\ngo 1.21\n")
	sub := filepath.Join(dir, "pkg", "utils")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	got := LibPrefixFromGoMod(sub)
	if got != "mylib" {
		t.Errorf("expected %q, got %q", "mylib", got)
	}
}

func TestLibPrefixFromGoMod_Missing(t *testing.T) {
	dir := t.TempDir()
	got := LibPrefixFromGoMod(dir)
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TransformFile — core transformation cases
// ─────────────────────────────────────────────────────────────────────────────

func TestTransformFile_BareReturn(t *testing.T) {
	src := `package mypkg

import "os"

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	if r.Err != nil {
		t.Fatalf("unexpected error: %v", r.Err)
	}
	assertContains(t, r.Patch,
		`fmt.Errorf("[mylib.mypkg.ReadFile] os.ReadFile error: %w", err)`,
		"patch")
}

func TestTransformFile_MultipleReturnValues(t *testing.T) {
	src := `package svc

import "encoding/json"

func Parse(data []byte) (string, int, error) {
	var rec struct{ Name string; ID int }
	err := json.Unmarshal(data, &rec)
	if err != nil {
		return "", 0, err
	}
	return rec.Name, rec.ID, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "svc.go", src)
	r := TransformFile(path, dir, "lib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	assertContains(t, r.Patch,
		`fmt.Errorf("[lib.svc.Parse] json.Unmarshal error: %w", err)`,
		"patch")
}

func TestTransformFile_ReceiverMethod(t *testing.T) {
	src := `package doi

import "github.com/example/zenodo"

type ZenodoProvider struct{ Verbose int }

func (z *ZenodoProvider) MakePublic(doi string) error {
	records, err := zenodo.DepositRecords(doi)
	if err != nil {
		return err
	}
	_ = records
	return nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "zenodo.go", src)
	r := TransformFile(path, dir, "golib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	assertContains(t, r.Patch,
		`fmt.Errorf("[golib.doi.ZenodoProvider.MakePublic] zenodo.DepositRecords error: %w", err)`,
		"patch")
}

func TestTransformFile_AlternativeErrVarNames(t *testing.T) {
	src := `package db

import "database/sql"

func Open(dsn string) (*sql.DB, error) {
	db, dberr := sql.Open("postgres", dsn)
	if dberr != nil {
		return nil, dberr
	}
	return db, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "db.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	assertContains(t, r.Patch,
		`fmt.Errorf("[mylib.db.Open] sql.Open error: %w", dberr)`,
		"patch")
}

func TestTransformFile_NoCalleeFound(t *testing.T) {
	// err is declared far above — callee inference gives up after 15 lines
	lines := []string{
		"package svc\n",
		"\n",
		"func Process() error {\n",
		"\tvar err error\n",
	}
	for i := 0; i < 20; i++ {
		lines = append(lines, "\t// padding line\n")
	}
	lines = append(lines,
		"\tif err != nil {\n",
		"\t\treturn err\n",
		"\t}\n",
		"\treturn nil\n",
		"}\n",
	)
	src := strings.Join(lines, "")

	dir := t.TempDir()
	path := writeFile(t, dir, "svc.go", src)
	r := TransformFile(path, dir, "lib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	// No callee → short form
	assertContains(t, r.Patch,
		`fmt.Errorf("[lib.svc.Process] error: %w", err)`,
		"patch")
}

func TestTransformFile_AlreadyWrapped_Unchanged(t *testing.T) {
	src := `package mypkg

import (
	"fmt"
	"os"
)

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("[mypkg.ReadFile] os.ReadFile error: %w", err)
	}
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if r.Changed {
		t.Error("expected no changes — already wrapped")
	}
}

func TestTransformFile_ThinWrapper_Unchanged(t *testing.T) {
	src := `package mypkg

func Wrap() error {
	return doWork()
}

func doWork() error {
	return nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "wrap.go", src)
	r := TransformFile(path, dir, "mylib")

	if r.Changed {
		t.Error("expected no changes — thin wrapper should be skipped")
	}
}

func TestTransformFile_ErrorDotError_Unchanged(t *testing.T) {
	// err.Error() returns a string, not an error — must not be transformed
	src := `package mypkg

func Describe(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "desc.go", src)
	r := TransformFile(path, dir, "mylib")

	if r.Changed {
		t.Error("expected no changes — err.Error() is a string return")
	}
}

func TestTransformFile_FmtImportAdded(t *testing.T) {
	// File has no "fmt" import; transformer must inject one
	src := `package mypkg

import "os"

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	assertContains(t, r.Patch, `"fmt"`, "patch must add fmt import")
}

func TestTransformFile_FmtImportNotDuplicated(t *testing.T) {
	// File already has "fmt"; transformer must not add a second one
	src := `package mypkg

import (
	"fmt"
	"os"
)

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_ = fmt.Sprintf("")
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected return to be transformed")
	}
	// Count how many times "fmt" appears in the + lines of the patch
	addedFmt := 0
	for _, line := range strings.Split(r.Patch, "\n") {
		if len(line) >= 2 && line[0] == '+' && line[1] != '+' && strings.Contains(line, `"fmt"`) {
			addedFmt++
		}
	}
	if addedFmt > 0 {
		t.Errorf("fmt import should not be re-added; found %d new \"fmt\" line(s) in patch", addedFmt)
	}
}

func TestTransformFile_NoPrefix(t *testing.T) {
	// Passing empty prefix produces [pkg.FuncName] without any library segment
	src := `package auth

import "errors"

func Validate(token string) error {
	err := checkToken(token)
	if err != nil {
		return err
	}
	return nil
}

func checkToken(token string) error {
	if token == "" {
		return errors.New("empty token")
	}
	return nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "auth.go", src)
	r := TransformFile(path, dir, "") // empty prefix

	if !r.Changed {
		t.Fatal("expected file to be changed")
	}
	assertContains(t, r.Patch, `[auth.Validate]`, "no-prefix location tag")
	assertNotContains(t, r.Patch, `[.auth.`, "must not have leading dot")
}

func TestTransformFile_PatchIsValidUnifiedDiff(t *testing.T) {
	src := `package mypkg

import "os"

func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	if !strings.HasPrefix(r.Patch, "--- ") {
		t.Error("patch must start with '--- '")
	}
	if !strings.Contains(r.Patch, "+++ ") {
		t.Error("patch must contain '+++ '")
	}
	if !strings.Contains(r.Patch, "@@ ") {
		t.Error("patch must contain a @@ hunk header")
	}
}

func TestTransformFile_MissingFile(t *testing.T) {
	r := TransformFile("/nonexistent/path/file.go", "/nonexistent", "mylib")
	if r.Err == nil {
		t.Error("expected an error for nonexistent file")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TransformFile — location tag construction
// ─────────────────────────────────────────────────────────────────────────────

func TestTransformFile_LocationTag_WithLibPkgReceiverFunc(t *testing.T) {
	src := `package config

type Manager struct{}

func (m *Manager) Load(path string) error {
	err := readConfig(path)
	if err != nil {
		return err
	}
	return nil
}

func readConfig(path string) error { return nil }
`
	dir := t.TempDir()
	path := writeFile(t, dir, "config.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	assertContains(t, r.Patch, "[mylib.config.Manager.Load]", "full location tag")
}

func TestTransformFile_LocationTag_WithLibPkgFunc(t *testing.T) {
	src := `package utils

import "strconv"

func ParseInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	return n, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "utils.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	assertContains(t, r.Patch, "[mylib.utils.ParseInt]", "lib.pkg.func location")
	assertContains(t, r.Patch, "strconv.Atoi error: %w", "callee in message")
}

func TestTransformFile_LocationTag_PointerReceiverStripped(t *testing.T) {
	// Receiver "s *MyService" → type should be "MyService", not "*MyService"
	src := `package svc

type MyService struct{}

func (s *MyService) Connect() error {
	err := dial()
	if err != nil {
		return err
	}
	return nil
}

func dial() error { return nil }
`
	dir := t.TempDir()
	path := writeFile(t, dir, "svc.go", src)
	r := TransformFile(path, dir, "lib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	assertContains(t, r.Patch, "[lib.svc.MyService.Connect]", "pointer stripped from receiver")
	// The generated fmt.Errorf line (a '+' line) must not contain "*MyService"
	for _, line := range strings.Split(r.Patch, "\n") {
		if len(line) >= 2 && line[0] == '+' && line[1] != '+' &&
			strings.Contains(line, "fmt.Errorf") &&
			strings.Contains(line, "*MyService") {
			t.Errorf("star must be stripped from receiver in generated line:\n%s", line)
		}
	}
}

func TestTransformFile_LocationTag_ValueReceiver(t *testing.T) {
	// Value receiver (no pointer): func (c Client) Do() error
	src := `package http

type Client struct{}

func (c Client) Do() error {
	err := send()
	if err != nil {
		return err
	}
	return nil
}

func send() error { return nil }
`
	dir := t.TempDir()
	path := writeFile(t, dir, "client.go", src)
	r := TransformFile(path, dir, "lib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	assertContains(t, r.Patch, "[lib.http.Client.Do]", "value receiver in tag")
}

// ─────────────────────────────────────────────────────────────────────────────
// TransformDir
// ─────────────────────────────────────────────────────────────────────────────

func TestTransformDir_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "a/a.go", `package a

import "os"

func ReadA(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`)
	writeFile(t, dir, "b/b.go", `package b

import "os"

func ReadB(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`)

	results := TransformDir(dir, "mylib")

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("unexpected error in %s: %v", r.Path, r.Err)
		}
		if !r.Changed {
			t.Errorf("expected %s to be changed", r.Path)
		}
	}
}

func TestTransformDir_TestFilesSkipped(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "pkg/pkg.go", `package pkg

import "os"

func Read(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`)
	// This test file should be skipped entirely
	writeFile(t, dir, "pkg/pkg_test.go", `package pkg

import "os"

func TestRead(t *testing.T) {
	_, err := os.ReadFile("x")
	if err != nil {
		_ = err
	}
}
`)

	results := TransformDir(dir, "mylib")

	if len(results) != 1 {
		t.Fatalf("expected 1 result (test file skipped), got %d", len(results))
	}
	if strings.HasSuffix(results[0].Path, "_test.go") {
		t.Error("test file should not appear in results")
	}
}

func TestTransformDir_VendorSkipped(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, dir, "pkg/pkg.go", `package pkg

import "os"

func Read(p string) ([]byte, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`)
	// vendor file — must be ignored
	writeFile(t, dir, "vendor/ext/ext.go", `package ext

import "os"

func Ext(p string) error {
	_, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return nil
}
`)

	results := TransformDir(dir, "mylib")

	if len(results) != 1 {
		t.Fatalf("expected 1 result (vendor skipped), got %d", len(results))
	}
	for _, r := range results {
		if strings.Contains(r.Path, "vendor") {
			t.Errorf("vendor path must not appear in results: %s", r.Path)
		}
	}
}

func TestTransformDir_NothingToChange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "clean.go", `package clean

func Hello() string { return "hello" }
`)

	results := TransformDir(dir, "mylib")

	if len(results) != 0 {
		t.Errorf("expected 0 results for file with no error returns, got %d", len(results))
	}
}

func TestTransformDir_AutoDetectPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module github.com/acme/myservice\n\ngo 1.21\n")
	writeFile(t, dir, "pkg/svc.go", `package pkg

import "os"

func Run(p string) error {
	_, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return nil
}
`)

	// Pass "" so the prefix is auto-detected from go.mod
	results := TransformDir(dir, "")

	if len(results) != 1 || !results[0].Changed {
		t.Fatal("expected one changed file")
	}
	assertContains(t, results[0].Patch, "[myservice.pkg.Run]", "auto-detected prefix")
}

// ─────────────────────────────────────────────────────────────────────────────
// Diff correctness
// ─────────────────────────────────────────────────────────────────────────────

func TestTransformFile_PatchLineNumbers(t *testing.T) {
	// 10-line file; the return is on line 9.
	// The @@ header must reference a line in that vicinity, not always "1".
	src := `package mypkg

import "os"

// line 5
// line 6
// line 7
func ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}
`
	dir := t.TempDir()
	path := writeFile(t, dir, "file.go", src)
	r := TransformFile(path, dir, "mylib")

	if !r.Changed {
		t.Fatal("expected change")
	}
	// The hunk header should NOT be @@ -1, ... for a change this deep in the file
	if strings.Contains(r.Patch, "@@ -1,") {
		// Only acceptable if the context truly starts at line 1 — but this file
		// has the return on line 11, so the hunk shouldn't start at line 1 unless
		// the context window happens to reach there (ctx=3, return on line 11 →
		// hunk starts at line 8 in the old file).  Line 1 would be wrong.
		firstHunk := r.Patch[strings.Index(r.Patch, "@@"):]
		if strings.HasPrefix(firstHunk, "@@ -1,1 ") {
			t.Error("hunk header claims the change is on line 1; line numbers appear wrong")
		}
	}
}

func TestUnifiedDiff_IdenticalFiles(t *testing.T) {
	src := "package x\n\nfunc F() {}\n"
	result := unifiedDiff(src, src, "a/x.go", "b/x.go")
	if result != "" {
		t.Errorf("expected empty diff for identical files, got:\n%s", result)
	}
}

func TestUnifiedDiff_EmptyToContent(t *testing.T) {
	result := unifiedDiff("", "line1\nline2\n", "a/x.go", "b/x.go")
	if !strings.Contains(result, "+line1") {
		t.Errorf("expected '+line1' in diff, got:\n%s", result)
	}
}

func TestUnifiedDiff_ContentToEmpty(t *testing.T) {
	result := unifiedDiff("line1\nline2\n", "", "a/x.go", "b/x.go")
	if !strings.Contains(result, "-line1") {
		t.Errorf("expected '-line1' in diff, got:\n%s", result)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal unit tests (white-box)
// ─────────────────────────────────────────────────────────────────────────────

func TestSplitLines(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"a\nb\nc\n", []string{"a\n", "b\n", "c\n"}},
		{"a\nb\nc", []string{"a\n", "b\n", "c"}}, // no trailing newline
		{"", nil},
		{"\n", []string{"\n"}},
	}
	for _, tc := range cases {
		got := splitLines(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("splitLines(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitLines(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestPackageName(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"package mypkg\n\nfunc F() {}\n", "mypkg"},
		{"// comment\npackage foo\n", "foo"},
		{"no package here\n", ""},
	}
	for _, tc := range cases {
		lines := splitLines(tc.src)
		got := packageName(lines)
		if got != tc.want {
			t.Errorf("packageName(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

func TestParseFunctions_PlainFunc(t *testing.T) {
	src := `package p

func Foo(x int) error {
	return nil
}
`
	funcs := parseFunctions(splitLines(src))
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(funcs))
	}
	f := funcs[0]
	if f.name != "Foo" {
		t.Errorf("name = %q, want %q", f.name, "Foo")
	}
	if f.receiver != "" {
		t.Errorf("receiver = %q, want empty", f.receiver)
	}
}

func TestParseFunctions_MethodWithReceiver(t *testing.T) {
	src := `package p

type T struct{}

func (t *T) Bar() error {
	return nil
}
`
	funcs := parseFunctions(splitLines(src))
	if len(funcs) != 1 {
		t.Fatalf("expected 1 function, got %d", len(funcs))
	}
	f := funcs[0]
	if f.name != "Bar" {
		t.Errorf("name = %q, want %q", f.name, "Bar")
	}
	if !strings.Contains(f.receiver, "T") {
		t.Errorf("receiver = %q, expected to contain 'T'", f.receiver)
	}
}

func TestParseFunctions_Multiple(t *testing.T) {
	src := `package p

func A() {}
func B() {}
func C() {}
`
	funcs := parseFunctions(splitLines(src))
	if len(funcs) != 3 {
		t.Fatalf("expected 3 functions, got %d", len(funcs))
	}
	names := []string{funcs[0].name, funcs[1].name, funcs[2].name}
	for i, want := range []string{"A", "B", "C"} {
		if names[i] != want {
			t.Errorf("funcs[%d].name = %q, want %q", i, names[i], want)
		}
	}
}

func TestIsThinWrapper_True(t *testing.T) {
	lines := splitLines(`func Foo() error {
	return doWork()
}
`)
	if !isThinWrapper(lines) {
		t.Error("expected isThinWrapper = true for single-delegation function")
	}
}

func TestIsThinWrapper_False_MultiLine(t *testing.T) {
	lines := splitLines(`func Foo() error {
	x := compute()
	err := doWork(x)
	if err != nil {
		return err
	}
	return nil
}
`)
	if isThinWrapper(lines) {
		t.Error("expected isThinWrapper = false for multi-statement function")
	}
}

func TestIsThinWrapper_False_NoCall(t *testing.T) {
	lines := splitLines(`func Foo() error {
	return nil
}
`)
	if isThinWrapper(lines) {
		t.Error("expected isThinWrapper = false — 'return nil' has no call")
	}
}

func TestBuildLocation_FullFourParts(t *testing.T) {
	fi := funcInfo{name: "MakePublic", receiver: "z *ZenodoProvider"}
	got := buildLocation("golib", "doi", fi)
	want := "[golib.doi.ZenodoProvider.MakePublic]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildLocation_NoReceiver(t *testing.T) {
	fi := funcInfo{name: "ParseConfig"}
	got := buildLocation("golib", "config", fi)
	want := "[golib.config.ParseConfig]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildLocation_NoPrefix(t *testing.T) {
	fi := funcInfo{name: "Run"}
	got := buildLocation("", "svc", fi)
	want := "[svc.Run]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildLocation_NoPrefixNoPackage(t *testing.T) {
	fi := funcInfo{name: "Run"}
	got := buildLocation("", "", fi)
	want := "[Run]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestBuildLocation_ValueReceiver(t *testing.T) {
	fi := funcInfo{name: "Do", receiver: "c Client"}
	got := buildLocation("lib", "http", fi)
	want := "[lib.http.Client.Do]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestInferCallee_ExternalPackage(t *testing.T) {
	src := `package p

import "encoding/json"

func Parse(data []byte) error {
	var v interface{}
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	return nil
}
`
	lines := splitLines(src)
	funcs := parseFunctions(lines)
	if len(funcs) == 0 {
		t.Fatal("no functions parsed")
	}

	// Find the return line
	retLine := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "return err" {
			retLine = i
			break
		}
	}
	if retLine < 0 {
		t.Fatal("could not find return line")
	}

	callee := inferCallee(lines, retLine, funcs[0].start, "err")
	if callee != "json.Unmarshal" {
		t.Errorf("inferCallee = %q, want %q", callee, "json.Unmarshal")
	}
}

func TestInferCallee_InternalFunc(t *testing.T) {
	src := `package p

func Process() error {
	err := validate()
	if err != nil {
		return err
	}
	return nil
}

func validate() error { return nil }
`
	lines := splitLines(src)
	funcs := parseFunctions(lines)

	retLine := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "return err" {
			retLine = i
			break
		}
	}
	if retLine < 0 {
		t.Fatal("could not find return line")
	}

	callee := inferCallee(lines, retLine, funcs[0].start, "err")
	if callee != "validate" {
		t.Errorf("inferCallee = %q, want %q", callee, "validate")
	}
}

func TestInferCallee_NotFound(t *testing.T) {
	lines := splitLines(`package p

func Process() error {
	var err error
	return err
}
`)
	funcs := parseFunctions(lines)
	retLine := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "return err" {
			retLine = i
			break
		}
	}
	callee := inferCallee(lines, retLine, funcs[0].start, "err")
	if callee != "" {
		t.Errorf("expected empty callee, got %q", callee)
	}
}

func TestHasFmtImport_InBlock(t *testing.T) {
	lines := splitLines(`package p

import (
	"fmt"
	"os"
)
`)
	if !hasFmtImport(lines) {
		t.Error("expected hasFmtImport = true")
	}
}

func TestHasFmtImport_SingleLine(t *testing.T) {
	lines := splitLines(`package p

import "fmt"
`)
	if !hasFmtImport(lines) {
		t.Error("expected hasFmtImport = true for single-line import")
	}
}

func TestHasFmtImport_False(t *testing.T) {
	lines := splitLines(`package p

import "os"
`)
	if hasFmtImport(lines) {
		t.Error("expected hasFmtImport = false")
	}
}

func TestAddFmtImport_IntoBlock(t *testing.T) {
	lines := splitLines(`package p

import (
	"os"
)
`)
	result := strings.Join(addFmtImport(lines), "")
	if !strings.Contains(result, `"fmt"`) {
		t.Errorf("expected fmt in result:\n%s", result)
	}
	if strings.Count(result, `"fmt"`) > 1 {
		t.Error("fmt inserted more than once")
	}
}

func TestAddFmtImport_SingleLineExpandsToBlock(t *testing.T) {
	lines := splitLines(`package p

import "os"

func F() {}
`)
	result := strings.Join(addFmtImport(lines), "")
	if !strings.Contains(result, `"fmt"`) {
		t.Errorf("expected fmt in result:\n%s", result)
	}
	if !strings.Contains(result, `"os"`) {
		t.Errorf("os import must be preserved:\n%s", result)
	}
	// Should now be a block import
	if !strings.Contains(result, "import (") {
		t.Errorf("expected block import after expansion:\n%s", result)
	}
}

func TestAddFmtImport_NoImport(t *testing.T) {
	lines := splitLines(`package p

func F() {}
`)
	result := strings.Join(addFmtImport(lines), "")
	if !strings.Contains(result, `"fmt"`) {
		t.Errorf("expected fmt injected when no imports exist:\n%s", result)
	}
}
