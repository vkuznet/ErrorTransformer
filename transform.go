// Package errortransformer rewrites bare "return err" statements in Go source
// files to use fmt.Errorf with a structured location prefix and %w wrapping,
// enabling errors.Is / errors.As chains while making error origin clear.
//
// Generated format:
//
//	[lib.pkg.ReceiverType.FuncName] callee.Call error: %w   (method with receiver)
//	[lib.pkg.FuncName] callee.Call error: %w                (plain function)
//	[lib.pkg.FuncName] error: %w                            (no callee found)
package errortransformer

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Known error variable names
// ─────────────────────────────────────────────────────────────────────────────

var errorVarNames = map[string]bool{
	"err": true, "error": true, "dberr": true, "e": true, "er": true,
	"derr": true, "serr": true, "ferr": true, "cerr": true, "ierr": true,
	"werr": true, "rerr": true, "merr": true, "nerr": true, "terr": true,
	"uerr": true, "perr": true, "aerr": true, "berr": true, "herr": true,
	"kerr": true, "lerr": true, "oerr": true, "qerr": true, "verr": true,
	"xerr": true, "yerr": true, "zerr": true, "err1": true, "err2": true,
	"err3": true, "myerr": true, "myErr": true,
}

// ─────────────────────────────────────────────────────────────────────────────
// Data types
// ─────────────────────────────────────────────────────────────────────────────

// funcInfo records the extent and identity of one Go function.
type funcInfo struct {
	start    int    // 0-based line index of "func" declaration
	end      int    // 0-based line index of closing "}"
	name     string // function name
	receiver string // raw receiver spec, e.g. "m *ZenodoProvider"
}

// FileResult holds the outcome of processing one Go file.
type FileResult struct {
	Path    string
	Changed bool
	Patch   string // unified diff text; empty when unchanged
	Err     error
}

// ─────────────────────────────────────────────────────────────────────────────
// Compiled regexps
// ─────────────────────────────────────────────────────────────────────────────

var (
	reFuncDecl     = regexp.MustCompile(`^func\s+(?:\(([^)]*)\)\s+)?(\w+)\s*\(`)
	rePackageLine  = regexp.MustCompile(`^package\s+(\w+)`)
	reImportOpen   = regexp.MustCompile(`^import\s*\(`)
	reImportSingle = regexp.MustCompile(`^import\s+"`)
	reModuleLine   = regexp.MustCompile(`^module\s+(\S+)`)
	reReturn       = buildReturnPattern()
)

// buildReturnPattern constructs a regexp that matches return lines whose last
// token is a known error variable name.
func buildReturnPattern() *regexp.Regexp {
	names := make([]string, 0, len(errorVarNames))
	for n := range errorVarNames {
		names = append(names, regexp.QuoteMeta(n))
	}
	// Longest name first so "dberr" beats "err"
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if len(names[j]) > len(names[i]) {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return regexp.MustCompile(
		`^(\s*return\s+)(.*?,\s*)?` +
			`(` + strings.Join(names, "|") + `)` +
			`(\s*)$`,
	)
}

// ─────────────────────────────────────────────────────────────────────────────
// Public API
// ─────────────────────────────────────────────────────────────────────────────

// TransformDir walks root recursively, transforms every non-test .go file, and
// returns one FileResult per changed file (or per file that had a processing
// error). libPrefix overrides auto-detection; pass "" to auto-detect from go.mod.
func TransformDir(root, libPrefix string) []FileResult {
	if libPrefix == "" {
		libPrefix = LibPrefixFromGoMod(root)
	}

	var results []FileResult
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			base := info.Name()
			if base != "." && (strings.HasPrefix(base, ".") || base == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		r := TransformFile(path, root, libPrefix)
		if r.Changed || r.Err != nil {
			results = append(results, r)
		}
		return nil
	})
	if err != nil {
		results = append(results, FileResult{Path: root, Err: err})
	}
	return results
}

// TransformFile reads one Go file, applies the transformation, and returns a
// FileResult. root is used to compute the relative path in the diff header.
func TransformFile(path, root, libPrefix string) FileResult {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileResult{Path: path, Err: err}
	}
	original := string(data)
	lines := splitLines(original)

	pkg := packageName(lines)
	funcs := parseFunctions(lines)

	// Map every line index to the index of the function that contains it
	lineToFunc := make(map[int]int, len(lines))
	for idx, fi := range funcs {
		for ln := fi.start; ln <= fi.end; ln++ {
			lineToFunc[ln] = idx
		}
	}

	newLines := make([]string, len(lines))
	copy(newLines, lines)

	changed := false
	fmtNeeded := false

	for i, line := range lines {
		if !strings.Contains(line, "return") {
			continue
		}

		stripped := strings.TrimRight(line, " \t\r\n")
		m := reReturn.FindStringSubmatch(stripped)
		if m == nil {
			continue
		}
		// m[1]=indent+"return ", m[2]=comma-prefix (may be ""), m[3]=errVar, m[4]=trailing ws
		prefix := m[2]
		errVar := m[3]

		// Skip already-wrapped lines
		if strings.Contains(stripped, "fmt.Errorf") || strings.Contains(stripped, "errors.") {
			continue
		}

		// Skip err.Error() – that's a string, not an error value
		afterVar := stripped[strings.LastIndex(stripped, errVar)+len(errVar):]
		if strings.HasPrefix(strings.TrimSpace(afterVar), ".") {
			continue
		}

		fiIdx, ok := lineToFunc[i]
		if !ok {
			continue
		}
		fi := funcs[fiIdx]

		if isThinWrapper(lines[fi.start : fi.end+1]) {
			continue
		}

		// Only wrap returns that are guarded by  if errVar != nil { ... }
		// Unguarded returns (happy-path returns like "return token, err") must
		// not be wrapped — err is nil there and wrapping produces %!w(<nil>).
		if !isGuardedReturn(lines, i, errVar) {
			continue
		}

		location := buildLocation(libPrefix, pkg, fi)
		callee := inferCallee(lines, i, fi.start, errVar)

		var msg string
		if callee != "" {
			msg = fmt.Sprintf("%s %s error: %%w", location, callee)
		} else {
			msg = fmt.Sprintf("%s error: %%w", location)
		}

		leadWS := leadingWhitespace(line)
		newExpr := fmt.Sprintf(`fmt.Errorf("%s", %s)`, msg, errVar)

		var newLine string
		if prefix != "" {
			newLine = leadWS + "return " + prefix + newExpr + "\n"
		} else {
			newLine = leadWS + "return " + newExpr + "\n"
		}

		newLines[i] = newLine
		changed = true
		fmtNeeded = true
	}

	if !changed {
		return FileResult{Path: path, Changed: false}
	}

	if fmtNeeded && !hasFmtImport(lines) {
		newLines = addFmtImport(newLines)
	}

	transformed := strings.Join(newLines, "")

	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}
	patch := unifiedDiff(original, transformed, "a/"+rel, "b/"+rel)

	return FileResult{Path: path, Changed: true, Patch: patch}
}

// LibPrefixFromGoMod walks up from dir looking for go.mod and returns the last
// path segment of the module declaration, e.g. "golib" for
// "github.com/CHESSComputing/golib".
func LibPrefixFromGoMod(dir string) string {
	search := dir
	for i := 0; i < 6; i++ {
		data, err := os.ReadFile(filepath.Join(search, "go.mod"))
		if err == nil {
			scanner := bufio.NewScanner(bytes.NewReader(data))
			for scanner.Scan() {
				m := reModuleLine.FindStringSubmatch(scanner.Text())
				if m != nil {
					mod := strings.TrimRight(m[1], "/")
					parts := strings.Split(mod, "/")
					return parts[len(parts)-1]
				}
			}
		}
		parent := filepath.Dir(search)
		if parent == search {
			break
		}
		search = parent
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// Source analysis helpers
// ─────────────────────────────────────────────────────────────────────────────

// splitLines splits src into lines preserving each trailing newline.
func splitLines(src string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lines = append(lines, src[start:i+1])
			start = i + 1
		}
	}
	if start < len(src) {
		lines = append(lines, src[start:])
	}
	return lines
}

// packageName returns the Go package name from the first "package X" line.
func packageName(lines []string) string {
	for _, l := range lines {
		m := rePackageLine.FindStringSubmatch(strings.TrimSpace(l))
		if m != nil {
			return m[1]
		}
	}
	return ""
}

// parseFunctions scans lines and returns all function extents.
func parseFunctions(lines []string) []funcInfo {
	var funcs []funcInfo
	depth := 0
	inFunc := false
	var cur funcInfo

	for i, rawLine := range lines {
		line := strings.TrimRight(rawLine, "\r\n")

		if !inFunc {
			m := reFuncDecl.FindStringSubmatch(line)
			if m != nil {
				cur = funcInfo{start: i, receiver: m[1], name: m[2]}
				opens := strings.Count(line, "{")
				closes := strings.Count(line, "}")
				depth = opens - closes
				if depth <= 0 && opens > 0 {
					// Single-line function body: func F() { ... }
					cur.end = i
					funcs = append(funcs, cur)
				} else {
					// Body spans multiple lines (depth>0) or opening brace not yet seen (opens==0)
					inFunc = true
				}
			}
		} else {
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				cur.end = i
				funcs = append(funcs, cur)
				inFunc = false
				depth = 0
			}
		}
	}
	return funcs
}

// isThinWrapper returns true when the function body contains only a single
// delegated return call, e.g.: func Foo() error { return Bar() }
func isThinWrapper(funcLines []string) bool {
	var inner []string
	for _, l := range funcLines {
		s := strings.TrimSpace(l)
		switch {
		case s == "", strings.HasPrefix(s, "//"):
			continue
		case strings.HasPrefix(s, "func "), s == "{", s == "}":
			continue
		default:
			inner = append(inner, s)
		}
	}
	if len(inner) == 1 && strings.HasPrefix(inner[0], "return ") {
		return strings.Contains(strings.TrimPrefix(inner[0], "return "), "(")
	}
	return false
}

// inferCallee scans backwards from retLine to find the assignment that produced
// errVar, returning the callee name exactly as written, e.g. "zenodo.AddRecord".
func inferCallee(lines []string, retLine, funcStart int, errVar string) string {
	pat := regexp.MustCompile(
		`(?:^|[\s,;(])` + regexp.QuoteMeta(errVar) + `\s*(?::=|=)\s*([\w][\w.]*)\s*\(`,
	)
	limit := retLine - 15
	if limit < funcStart {
		limit = funcStart
	}
	for i := retLine - 1; i >= limit; i-- {
		if i < 0 || i >= len(lines) {
			continue
		}
		m := pat.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m != nil {
			return m[1]
		}
	}
	return ""
}

// isGuardedReturn returns true when the return at retLine sits inside an
// "if <errVar> != nil" (or "if err != nil" variant) block.  It scans upward
// from retLine looking for the enclosing if-statement within a small window,
// accepting any of the common guard forms:
//
//	if err != nil {
//	if err != nil { ... }   (single-line)
//	if _, err = f(); err != nil {
func isGuardedReturn(lines []string, retLine int, errVar string) bool {
	// Match:  if [anything;] <errVar> != nil
	pat := regexp.MustCompile(
		`\bif\b.*\b` + regexp.QuoteMeta(errVar) + `\s*!=\s*nil`,
	)
	// Scan upward up to 5 lines (covers multiline if-headers and blank lines)
	for i := retLine - 1; i >= 0 && i >= retLine-5; i-- {
		s := strings.TrimSpace(lines[i])
		if s == "" || strings.HasPrefix(s, "//") {
			continue
		}
		if pat.MatchString(s) {
			return true
		}
		// Stop at any statement that isn't a closing brace or blank — we've
		// left the if-block.
		if s != "{" && s != "}" && !strings.HasPrefix(s, "if ") {
			break
		}
	}
	return false
}

// buildLocation constructs the bracket location tag.
func buildLocation(libPrefix, pkg string, fi funcInfo) string {
	recvType := ""
	if fi.receiver != "" {
		parts := strings.Fields(fi.receiver)
		if len(parts) > 0 {
			recvType = strings.TrimLeft(parts[len(parts)-1], "*")
		}
	}

	pkgFull := pkg
	switch {
	case libPrefix != "" && pkg != "":
		pkgFull = libPrefix + "." + pkg
	case libPrefix != "":
		pkgFull = libPrefix
	}

	switch {
	case pkgFull != "" && recvType != "" && fi.name != "":
		return fmt.Sprintf("[%s.%s.%s]", pkgFull, recvType, fi.name)
	case pkgFull != "" && fi.name != "":
		return fmt.Sprintf("[%s.%s]", pkgFull, fi.name)
	case fi.name != "":
		return fmt.Sprintf("[%s]", fi.name)
	default:
		return "[unknown]"
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Import manipulation
// ─────────────────────────────────────────────────────────────────────────────

func hasFmtImport(lines []string) bool {
	inBlock := false
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if reImportOpen.MatchString(s) {
			inBlock = true
			continue
		}
		if inBlock {
			if s == ")" {
				inBlock = false
			} else if strings.Contains(s, `"fmt"`) {
				return true
			}
			continue
		}
		if strings.HasPrefix(s, `import "fmt"`) {
			return true
		}
	}
	return false
}

func addFmtImport(lines []string) []string {
	blockOpen, blockClose := -1, -1
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if reImportOpen.MatchString(s) {
			blockOpen = i
		} else if blockOpen >= 0 && blockClose < 0 && s == ")" {
			blockClose = i
			break
		}
	}

	if blockOpen >= 0 && blockClose >= 0 {
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:blockOpen+1]...)
		out = append(out, "\t\"fmt\"\n")
		out = append(out, lines[blockOpen+1:]...)
		return out
	}

	// Single-line import → expand to block
	reQuoted := regexp.MustCompile(`"[^"]+"`)
	for i, l := range lines {
		s := strings.TrimSpace(l)
		if reImportSingle.MatchString(s) {
			existing := reQuoted.FindString(s)
			lines[i] = "import (\n\t\"fmt\"\n\t" + existing + "\n)\n"
			return lines
		}
	}

	// No import at all → insert after package declaration
	for i, l := range lines {
		if rePackageLine.MatchString(strings.TrimSpace(l)) {
			out := make([]string, 0, len(lines)+2)
			out = append(out, lines[:i+1]...)
			out = append(out, "\nimport \"fmt\"\n")
			out = append(out, lines[i+1:]...)
			return out
		}
	}
	return lines
}

func leadingWhitespace(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

// ─────────────────────────────────────────────────────────────────────────────
// Unified diff (stdlib only, correct absolute line numbers)
// ─────────────────────────────────────────────────────────────────────────────

// diffLine represents one entry in a Myers edit script.
type diffLine struct {
	tag  byte   // '=' equal  '-' remove  '+' add
	text string // line content including trailing newline
}

// unifiedDiff returns a unified diff string between oldSrc and newSrc.
func unifiedDiff(oldSrc, newSrc, oldLabel, newLabel string) string {
	a := splitLines(oldSrc)
	b := splitLines(newSrc)

	script := myersDiff(a, b)
	hunks := groupHunks(script, 3)
	if len(hunks) == 0 {
		return ""
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "--- %s\n", oldLabel)
	fmt.Fprintf(&buf, "+++ %s\n", newLabel)
	for _, h := range hunks {
		buf.WriteString(h)
	}
	return buf.String()
}

// groupHunks groups a flat edit script into unified-diff hunks with ctx lines
// of context and correct 1-based @@ line numbers.
func groupHunks(script []diffLine, ctx int) []string {
	// Locate all changed positions
	var changePos []int
	for i, d := range script {
		if d.tag != '=' {
			changePos = append(changePos, i)
		}
	}
	if len(changePos) == 0 {
		return nil
	}

	// Merge nearby changes into hunk ranges (indices into script slice)
	type hRange struct{ lo, hi int }
	var ranges []hRange
	lo := changePos[0] - ctx
	if lo < 0 {
		lo = 0
	}
	hi := changePos[0] + ctx + 1
	for _, p := range changePos[1:] {
		newLo := p - ctx
		if newLo < 0 {
			newLo = 0
		}
		newHi := p + ctx + 1
		if newLo <= hi {
			if newHi > hi {
				hi = newHi
			}
		} else {
			ranges = append(ranges, hRange{lo, hi})
			lo, hi = newLo, newHi
		}
	}
	ranges = append(ranges, hRange{lo, hi})

	var hunks []string
	for _, r := range ranges {
		end := r.hi
		if end > len(script) {
			end = len(script)
		}
		chunk := script[r.lo:end]

		// Compute 1-based line numbers by counting lines before this hunk
		oldStart, newStart := 1, 1
		for _, d := range script[:r.lo] {
			if d.tag == '=' || d.tag == '-' {
				oldStart++
			}
			if d.tag == '=' || d.tag == '+' {
				newStart++
			}
		}
		oldCount, newCount := 0, 0
		for _, d := range chunk {
			if d.tag == '=' || d.tag == '-' {
				oldCount++
			}
			if d.tag == '=' || d.tag == '+' {
				newCount++
			}
		}

		var buf strings.Builder
		fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for _, d := range chunk {
			switch d.tag {
			case '=':
				buf.WriteString(" " + d.text)
			case '-':
				buf.WriteString("-" + d.text)
			case '+':
				buf.WriteString("+" + d.text)
			}
		}
		hunks = append(hunks, buf.String())
	}
	return hunks
}

// myersDiff computes the shortest edit script between a and b using the Myers
// O(ND) algorithm with backtracking.
func myersDiff(a, b []string) []diffLine {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	if n == 0 {
		out := make([]diffLine, m)
		for i, l := range b {
			out[i] = diffLine{'+', l}
		}
		return out
	}
	if m == 0 {
		out := make([]diffLine, n)
		for i, l := range a {
			out[i] = diffLine{'-', l}
		}
		return out
	}

	maxD := n + m
	// v[k+maxD] = furthest x reached on diagonal k
	v := make([]int, 2*maxD+2)
	trace := make([][]int, 0, maxD+1)

	for d := 0; d <= maxD; d++ {
		snap := make([]int, len(v))
		copy(snap, v)
		trace = append(trace, snap)

		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[k-1+maxD] < v[k+1+maxD]) {
				x = v[k+1+maxD] // move down (insert from b)
			} else {
				x = v[k-1+maxD] + 1 // move right (delete from a)
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[k+maxD] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, d, maxD)
			}
		}
	}

	// Should be unreachable; safe fallback
	var out []diffLine
	for _, l := range a {
		out = append(out, diffLine{'-', l})
	}
	for _, l := range b {
		out = append(out, diffLine{'+', l})
	}
	return out
}

// backtrack reconstructs the edit script from the Myers trace.
func backtrack(trace [][]int, a, b []string, d, maxD int) []diffLine {
	x, y := len(a), len(b)
	edits := make([]diffLine, 0, d*2)

	for dd := d; dd > 0; dd-- {
		v := trace[dd]
		k := x - y
		var prevK int
		if k == -dd || (k != dd && v[k-1+maxD] < v[k+1+maxD]) {
			prevK = k + 1 // came from above: insert
		} else {
			prevK = k - 1 // came from left: delete
		}
		prevX := v[prevK+maxD]
		prevY := prevX - prevK

		// Diagonal (equal) moves from (prevX,prevY) to (x,y) minus the one step
		for x > prevX && y > prevY {
			x--
			y--
			edits = append([]diffLine{{'=', a[x]}}, edits...)
		}
		if prevK == k+1 {
			// Came from k+1 diagonal (y increased): insert from b
			y--
			edits = append([]diffLine{{'+', b[y]}}, edits...)
		} else {
			// Came from k-1 diagonal (x increased): delete from a
			x--
			edits = append([]diffLine{{'-', a[x]}}, edits...)
		}
	}

	// Remaining equal prefix
	for x > 0 && y > 0 {
		x--
		y--
		edits = append([]diffLine{{'=', a[x]}}, edits...)
	}
	for x > 0 {
		x--
		edits = append([]diffLine{{'-', a[x]}}, edits...)
	}
	for y > 0 {
		y--
		edits = append([]diffLine{{'+', b[y]}}, edits...)
	}
	return edits
}
