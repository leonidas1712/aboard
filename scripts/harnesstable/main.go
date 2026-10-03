// Command harnesstable writes the README's harness support matrix from the harness
// profiles in adapters/ and the live kit's results in e2e/live/support.json, between
// the two harness-table comments in README.md. With -check it changes nothing and fails
// if the README's table is out of date. Run it from the repository's root, through make
// harness-table and make harness-table-check.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/leonidas1712/aboard/e2e/support"
)

const (
	readme = "README.md"
	start  = "<!-- harness-table: written by make harness-table from adapters/ and e2e/live/support.json -->\n"
	end    = "<!-- end of harness-table -->\n"
)

func main() {
	check := flag.Bool("check", false, "fail if README.md's table is out of date, without changing it")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "harness-table:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	profiles, err := support.Profiles()
	if err != nil {
		return err
	}
	results, err := support.LoadResults(support.ResultsFile)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(readme)
	if err != nil {
		return err
	}
	i, j := bytes.Index(old, []byte(start)), bytes.Index(old, []byte(end))
	if i < 0 || j < i {
		return fmt.Errorf("%s has no table between %q and %q", readme, start, end)
	}
	var out bytes.Buffer
	out.Write(old[:i+len(start)])
	out.WriteString("\n" + support.Table(profiles, results) + "\n")
	out.Write(old[j:])
	if bytes.Equal(out.Bytes(), old) {
		return nil
	}
	if check {
		return fmt.Errorf("the harness table in %s is out of date: run make harness-table", readme)
	}
	return os.WriteFile(readme, out.Bytes(), 0o644) //nolint:gosec // the repository's README
}
