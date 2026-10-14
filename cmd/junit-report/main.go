package main

import (
	"encoding/xml"
	"fmt"
	"html"
	"log"
	"os"
	"slices"
	"strings"
)

type testSuites struct {
	TestCases []testCase `xml:"testsuite>testcase"`
}

type testCase struct {
	Skipped *skipped `xml:"skipped"`
	Failure *failure `xml:"failure"`
	Name    string   `xml:"name,attr"`
}

type (
	skipped struct{}
	failure struct{}
)

// rootTest is the single top-level test of the conformance package, and the prefix every other case
// carries. It shortens the reported names; which cases are aggregates is decided structurally, by
// aggregateNames.
const rootTest = "TestConformance"

// aggregateNames returns the cases that contain others.
//
// `go test` marks a test failed as soon as one of its subtests is, so such a test aggregates its
// subtests rather than being a test of its own: counting it turns a single real failure into two,
// and a clean run into one extra pass. The relation is structural -- a case whose name is a prefix
// of another's, followed by a slash -- and it holds at every depth, not only at the root.
//
// A case with no subtest is a test, even when it is the root: a run that fails before creating any
// subtest is a failure and must be counted, which is what a setup failure looks like.
func aggregateNames(names []string) map[string]struct{} {
	sorted := slices.Clone(names)
	slices.Sort(sorted)

	aggregates := make(map[string]struct{})
	for i := 0; i+1 < len(sorted); i++ {
		if strings.HasPrefix(sorted[i+1], sorted[i]+"/") {
			aggregates[sorted[i]] = struct{}{}
		}
	}
	return aggregates
}

// summarize splits the test cases of a JUnit file into passed, skipped and failed, dropping the
// cases that aggregate others. The three partitions and the parse error are its return contract.
//
//revive:disable-next-line:function-result-limit
func summarize(data []byte) (passed, skipped, failed []string, err error) {
	var suites testSuites
	if err := xml.Unmarshal(data, &suites); err != nil {
		return nil, nil, nil, fmt.Errorf("parsing XML: %w", err)
	}

	names := make([]string, 0, len(suites.TestCases))
	for _, tc := range suites.TestCases {
		names = append(names, tc.Name)
	}
	aggregates := aggregateNames(names)

	for _, tc := range suites.TestCases {
		if _, isAggregate := aggregates[tc.Name]; isAggregate {
			continue
		}
		name := strings.TrimPrefix(tc.Name, rootTest+"/")
		switch {
		case tc.Failure != nil:
			failed = append(failed, name)
		case tc.Skipped != nil:
			skipped = append(skipped, name)
		default:
			passed = append(passed, name)
		}
	}
	slices.Sort(passed)
	slices.Sort(skipped)
	slices.Sort(failed)
	return passed, skipped, failed, nil
}

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: junit-report <junit.xml> [output.html]")
	}

	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		log.Fatalf("reading %s: %v", os.Args[1], err)
	}

	passed, skippedNames, failed, err := summarize(data)
	if err != nil {
		log.Fatalf("summarizing %s: %v", os.Args[1], err)
	}

	var sb strings.Builder
	_, _ = fmt.Fprintf(&sb, "PASSED  (%d):\n", len(passed))
	for _, n := range passed {
		_, _ = fmt.Fprintf(&sb, "  + %s\n", n)
	}
	_, _ = fmt.Fprintln(&sb)
	_, _ = fmt.Fprintf(&sb, "FAILED  (%d):\n", len(failed))
	for _, n := range failed {
		_, _ = fmt.Fprintf(&sb, "  x %s\n", n)
	}
	_, _ = fmt.Fprintln(&sb)
	_, _ = fmt.Fprintf(&sb, "SKIPPED (%d):\n", len(skippedNames))
	for _, n := range skippedNames {
		_, _ = fmt.Fprintf(&sb, "  - %s\n", n)
	}
	_, _ = fmt.Fprintln(&sb)
	_, _ = fmt.Fprintf(&sb, "TOTAL: %d passed, %d failed, %d skipped\n", len(passed), len(failed), len(skippedNames))

	output := sb.String()
	fmt.Print(output)

	if len(os.Args) >= 3 {
		if err := os.WriteFile(os.Args[2], []byte(renderHTML(passed, failed, skippedNames)), 0o600); err != nil {
			log.Fatalf("writing %s: %v", os.Args[2], err)
		}
		_, _ = fmt.Fprintf(os.Stderr, "HTML report written to %s\n", os.Args[2])
	}

	if len(failed) > 0 {
		os.Exit(1)
	}
}

func renderHTML(passed, failed, skipped []string) string {
	var b strings.Builder

	statusClass := "pass"
	statusLabel := "ALL PASSING"
	if len(failed) > 0 {
		statusClass = "fail"
		statusLabel = fmt.Sprintf("%d FAILING", len(failed))
	}

	b.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Conformance Report</title>
<style>
  body { font-family: monospace; background: #1e1e1e; color: #d4d4d4; margin: 2em; }
  h1 { color: #fff; }
  .summary { display: flex; gap: 2em; margin: 1em 0 2em; }
  .badge { padding: .4em 1em; border-radius: 4px; font-size: 1.1em; font-weight: bold; }
  .pass  { background: #1a4731; color: #4ec994; }
  .fail  { background: #4b1a1a; color: #f48771; }
  .skip  { background: #2d2d2d; color: #888; }
  details { margin-bottom: 1em; }
  summary { cursor: pointer; padding: .3em .5em; border-radius: 4px; font-size: 1em; user-select: none; }
  summary.pass  { background: #1a4731; color: #4ec994; }
  summary.fail  { background: #4b1a1a; color: #f48771; }
  summary.skip  { background: #2d2d2d; color: #888; }
  ul { margin: .5em 0 0 1em; padding: 0; list-style: none; }
  li { padding: .15em 0; font-size: .9em; }
  .status { background: #161616; border-radius: 3px; padding: .2em .6em; display: inline-block; margin-bottom: .5em; }
</style>
</head>
<body>
`)
	_, _ = fmt.Fprint(&b, "<h1>Gateway API Conformance Report</h1>\n")
	_, _ = fmt.Fprint(&b, `<div class="summary">`)
	_, _ = fmt.Fprintf(&b, `<span class="badge %s">%s</span>`, statusClass, statusLabel)
	_, _ = fmt.Fprintf(&b, `<span class="badge pass">%d passed</span>`, len(passed))
	_, _ = fmt.Fprintf(&b, `<span class="badge fail">%d failed</span>`, len(failed))
	_, _ = fmt.Fprintf(&b, `<span class="badge skip">%d skipped</span>`, len(skipped))
	_, _ = fmt.Fprint(&b, "</div>\n")

	writeSection(&b, "fail", fmt.Sprintf("Failed (%d)", len(failed)), failed, len(failed) > 0)
	writeSection(&b, "pass", fmt.Sprintf("Passed (%d)", len(passed)), passed, false)
	writeSection(&b, "skip", fmt.Sprintf("Skipped (%d)", len(skipped)), skipped, false)

	b.WriteString("</body>\n</html>\n")
	return b.String()
}

func writeSection(b *strings.Builder, class, title string, names []string, open bool) {
	openAttr := ""
	if open {
		openAttr = " open"
	}
	_, _ = fmt.Fprintf(b, "<details%s>\n<summary class=%q>%s</summary>\n<ul>\n", openAttr, class, html.EscapeString(title))
	for _, n := range names {
		_, _ = fmt.Fprintf(b, "<li>%s</li>\n", html.EscapeString(n))
	}
	b.WriteString("</ul>\n</details>\n")
}
