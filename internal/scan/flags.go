// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scan

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/tools/go/buildutil"
	"golang.org/x/vuln/internal/govulncheck"
)

type config struct {
	govulncheck.Config
	patterns []string
	mode     string
	db       string
	json     bool
	dir      string
	tags     []string
	test     bool
	show     []string
	env      []string
}

const (
	modeBinary  = "binary"
	modeSource  = "source"
	modeConvert = "convert" // only intended for use by gopls
	modeQuery   = "query"   // only intended for use by gopls
)

func parseFlags(cfg *config, stderr io.Writer, args []string) error {
	var tagsFlag buildutil.TagsFlag
	var showFlag showFlag
	flags := flag.NewFlagSet("", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.BoolVar(&cfg.json, "json", false, "output JSON")
	flags.BoolVar(&cfg.test, "test", false, "analyze test files (only valid for source mode)")
	flags.StringVar(&cfg.dir, "C", "", "change to `dir` before running govulncheck")
	flags.StringVar(&cfg.db, "db", "https://vuln.go.dev", "vulnerability database `url`")
	flags.StringVar(&cfg.mode, "mode", modeSource, "supports source or binary")
	flags.Var(&tagsFlag, "tags", "comma-separated `list` of build tags")
	flags.Var(&showFlag, "show", "enable display of additional information specified by the comma separated `list`\nThe only supported value is 'traces'")
	scanLevel := flags.String("scan", "symbol", "set the scanning level desired, one of module, package or symbol")
	flags.Usage = func() {
		fmt.Fprint(flags.Output(), `Govulncheck reports known vulnerabilities in dependencies.

Usage:

	govulncheck [flags] [patterns]
	govulncheck -mode=binary [flags] [binary]

`)
		flags.PrintDefaults()
		fmt.Fprintf(flags.Output(), "\n%s\n", detailsMessage)
	}
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return errHelp
		}
		return err
	}
	cfg.patterns = flags.Args()
	if cfg.mode != modeConvert && len(cfg.patterns) == 0 {
		flags.Usage()
		return errUsage
	}
	cfg.tags = tagsFlag
	cfg.show = showFlag
	cfg.ScanLevel = govulncheck.ScanLevel(*scanLevel)
	if err := validateConfig(cfg); err != nil {
		fmt.Fprintln(flags.Output(), err)
		return errUsage
	}
	return nil
}

var supportedModes = map[string]bool{
	modeSource:  true,
	modeBinary:  true,
	modeConvert: true,
	modeQuery:   true,
}

func validateConfig(cfg *config) error {
	if _, ok := supportedModes[cfg.mode]; !ok {
		return fmt.Errorf("%q is not a valid mode", cfg.mode)
	}
	switch cfg.mode {
	case modeSource:
		if len(cfg.patterns) == 1 && isFile(cfg.patterns[0]) {
			return fmt.Errorf("%q is a file.\n\n%v", cfg.patterns[0], errNoBinaryFlag)
		}
	case modeBinary:
		if cfg.test {
			return fmt.Errorf("the -test flag is not supported in binary mode")
		}
		if len(cfg.tags) > 0 {
			return fmt.Errorf("the -tags flag is not supported in binary mode")
		}
		if len(cfg.patterns) != 1 {
			return fmt.Errorf("only 1 binary can be analyzed at a time")
		}
		if !isFile(cfg.patterns[0]) {
			return fmt.Errorf("%q is not a file", cfg.patterns[0])
		}
	case modeConvert:
		if len(cfg.patterns) != 0 {
			return fmt.Errorf("patterns are not accepted in convert mode")
		}
		if cfg.dir != "" {
			return fmt.Errorf("the -C flag is not supported in convert mode")
		}
		if cfg.test {
			return fmt.Errorf("the -test flag is not supported in convert mode")
		}
		if len(cfg.tags) > 0 {
			return fmt.Errorf("the -tags flag is not supported in convert mode")
		}
	case modeQuery:
		if cfg.test {
			return fmt.Errorf("the -test flag is not supported in query mode")
		}
		if len(cfg.tags) > 0 {
			return fmt.Errorf("the -tags flag is not supported in query mode")
		}
		if !cfg.json {
			return fmt.Errorf("the -json flag must be set in query mode")
		}
		for _, pattern := range cfg.patterns {
			// Parse the input here so that we can catch errors before
			// outputting the Config.
			if _, _, err := parseModuleQuery(pattern); err != nil {
				return err
			}
		}
	}
	if cfg.json && len(cfg.show) > 0 {
		return fmt.Errorf("the -show flag is not supported for JSON output")
	}
	return nil
}

func isFile(path string) bool {
	s, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !s.IsDir()
}

// fileExists checks if file path exists. Returns true
// if the file exists or it cannot prove that it does
// not exist. Otherwise, returns false.
func fileExists(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return true
	} else if errors.Is(err, os.ErrNotExist) {
		return false
	}
	// Conservatively return true if os.Stat fails
	// for some other reason.
	return true
}

type showFlag []string

func (v *showFlag) Set(s string) error {
	*v = append(*v, strings.Split(s, ",")...)
	return nil
}

func (f *showFlag) Get() interface{} { return *f }
func (f *showFlag) String() string   { return "<options>" }
