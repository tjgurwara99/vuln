// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package scan

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/vuln/internal"
	"golang.org/x/vuln/internal/govulncheck"
	"golang.org/x/vuln/internal/osv"
)

type style int

const (
	defaultStyle = style(iota)
	goStyle
	scannerStyle
	osvCalledStyle
	osvImportedStyle
	detailsStyle
	sectionStyle
	keyStyle
	valueStyle
)

// NewtextHandler returns a handler that writes govulncheck output as text.
func NewTextHandler(w io.Writer) *TextHandler {
	return &TextHandler{w: w}
}

type TextHandler struct {
	w        io.Writer
	osvs     []*osv.Entry
	findings []*findingSummary

	err error

	showColor  bool
	showTraces bool
}

const (
	detailsMessage = `For details, see https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck.`

	binaryProgressMessage = `Scanning your binary for known vulnerabilities...`
)

func (h *TextHandler) Show(show []string) {
	for _, show := range show {
		switch show {
		case "traces":
			h.showTraces = true
		case "color":
			h.showColor = true
		}
	}
}

func Flush(h govulncheck.Handler) error {
	if th, ok := h.(interface{ Flush() error }); ok {
		return th.Flush()
	}
	return nil
}

func (h *TextHandler) Flush() error {
	fixupFindings(h.osvs, h.findings)
	h.byVulnerability(h.findings)
	h.summary(h.findings)
	h.print("\nShare feedback at https://go.dev/s/govulncheck-feedback.\n")
	if h.err != nil {
		return h.err
	}
	if isCalled(h.findings) {
		return errVulnerabilitiesFound
	}
	return nil
}

// Config writes text output formatted according to govulncheck-intro.tmpl.
func (h *TextHandler) Config(config *govulncheck.Config) error {
	h.print("Using ")
	if config.GoVersion != "" {
		h.style(goStyle, config.GoVersion)
		h.print(` and `)
	}
	if config.ScannerName != "" {
		h.style(scannerStyle, config.ScannerName)
		if config.ScannerVersion != "" {
			h.print(`@`, config.ScannerVersion)
		}
		h.print(` with `)
	}
	h.print(`vulnerability data from `, config.DB)
	if config.DBLastModified != nil {
		h.print(` (last modified `, *config.DBLastModified, `)`)
	}
	h.print(".\n\n")
	return h.err
}

// Progress writes progress updates during govulncheck execution..
func (h *TextHandler) Progress(progress *govulncheck.Progress) error {
	h.print(progress.Message, "\n\n")
	return h.err
}

// OSV gathers osv entries to be written.
func (h *TextHandler) OSV(entry *osv.Entry) error {
	h.osvs = append(h.osvs, entry)
	return nil
}

// Finding gathers vulnerability findings to be written.
func (h *TextHandler) Finding(finding *govulncheck.Finding) error {
	if err := validateFindings(finding); err != nil {
		return err
	}
	h.findings = append(h.findings, newFindingSummary(finding))
	return nil
}

func (h *TextHandler) byVulnerability(findings []*findingSummary) {
	byVuln := groupByVuln(findings)
	called := 0
	for _, findings := range byVuln {
		if isCalled(findings) {
			h.vulnerability(called, findings)
			called++
		}
	}
	unCalled := len(byVuln) - called
	if unCalled == 0 {
		return
	}
	h.style(sectionStyle, "=== Informational ===\n")
	h.print("\nFound ", unCalled)
	h.print(choose(unCalled == 1, ` vulnerability`, ` vulnerabilities`))
	h.print(" in packages that you import, but there are no call\nstacks leading to the use of ")
	h.print(choose(unCalled == 1, `this vulnerability`, `these vulnerabilities`))
	h.print(". You may not need to\ntake any action. See https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck\nfor details.\n\n")
	index := 0
	for _, findings := range byVuln {
		if !isCalled(findings) {
			h.vulnerability(index, findings)
			index++
		}
	}
}

func (h *TextHandler) vulnerability(index int, findings []*findingSummary) {
	h.style(keyStyle, "Vulnerability")
	h.print(" #", index+1, ": ")
	if isCalled(findings) {
		h.style(osvCalledStyle, findings[0].OSV.ID)
	} else {
		h.style(osvImportedStyle, findings[0].OSV.ID)
	}
	h.print("\n")
	h.style(detailsStyle)
	description := findings[0].OSV.Summary
	if description == "" {
		description = findings[0].OSV.Details
	}
	h.wrap("    ", description, 80)
	h.style(defaultStyle)
	h.print("\n")
	h.style(keyStyle, "  More info:")
	h.print(" ", findings[0].OSV.DatabaseSpecific.URL, "\n")

	byModule := groupByModule(findings)
	first := true
	for _, module := range byModule {
		//TODO: this assumes all traces on a module are found and fixed at the same versions
		lastFrame := module[0].Trace[0]
		mod := lastFrame.Module
		path := lastFrame.Module
		if path == internal.GoStdModulePath {
			path = lastFrame.Package
		}
		foundVersion := moduleVersionString(lastFrame.Module, lastFrame.Version)
		fixedVersion := moduleVersionString(lastFrame.Module, module[0].FixedVersion)
		if !first {
			h.print("\n")
		}
		first = false
		h.print("  ")
		if mod == internal.GoStdModulePath {
			h.print("Standard library")
		} else {
			h.style(keyStyle, "Module: ")
			h.print(mod)
		}
		h.print("\n    ")
		h.style(keyStyle, "Found in: ")
		h.print(path, "@", foundVersion, "\n    ")
		h.style(keyStyle, "Fixed in: ")
		if fixedVersion != "" {
			h.print(path, "@", fixedVersion)
		} else {
			h.print("N/A")
		}
		h.print("\n")
		platforms := platforms(mod, module[0].OSV)
		if len(platforms) > 0 {
			h.style(keyStyle, "    Platforms: ")
			for ip, p := range platforms {
				if ip > 0 {
					h.print(", ")
				}
				h.print(p)
			}
			h.print("\n")
		}
		h.traces(module)
	}
	h.print("\n")
}

func (h *TextHandler) traces(traces []*findingSummary) {
	first := true
	for i, entry := range traces {
		if entry.Compact == "" {
			continue
		}
		if first {
			h.style(keyStyle, "    Example traces found:\n")
		}
		first = false

		h.print("      #", i+1, ": ")
		if !h.showTraces {
			h.print(entry.Compact, "\n")
		} else {
			h.print("for function ", symbol(entry.Trace[0], false), "\n")
			for i := len(entry.Trace) - 1; i >= 0; i-- {
				t := entry.Trace[i]
				h.print("        ")
				if t.Position != nil {
					h.print(posToString(t.Position), ": ")
				}
				h.print(symbol(t, false), "\n")
			}
		}
	}
}

func (h *TextHandler) summary(findings []*findingSummary) {
	counters := counters(findings)
	if counters.VulnerabilitiesCalled == 0 {
		h.print("No vulnerabilities found.\n")
		return
	}
	h.print(`Your code is affected by `)
	h.style(valueStyle, counters.VulnerabilitiesCalled)
	h.print(choose(counters.VulnerabilitiesCalled == 1, ` vulnerability`, ` vulnerabilities`))
	h.print(` from`)
	if counters.ModulesCalled > 0 {
		h.print(` `)
		h.style(valueStyle, counters.ModulesCalled)
		h.print(choose(counters.ModulesCalled == 1, ` module`, ` modules`))
	}
	if counters.StdlibCalled {
		if counters.ModulesCalled != 0 {
			h.print(` and`)
		}
		h.print(` the Go standard library`)
	}
	h.print(".\n")
}

func (h *TextHandler) style(style style, values ...any) {
	if h.showColor {
		switch style {
		default:
			h.print(colorReset)
		case goStyle:
			h.print(colorBold)
		case scannerStyle:
			h.print(colorBold)
		case osvCalledStyle:
			h.print(colorBold, fgRed)
		case osvImportedStyle:
			h.print(colorBold, fgGreen)
		case detailsStyle:
			h.print(colorFaint)
		case sectionStyle:
			h.print(fgBlue)
		case keyStyle:
			h.print(colorFaint, fgYellow)
		case valueStyle:
			h.print(colorBold, fgCyan)
		}
	}
	h.print(values...)
	if h.showColor && len(values) > 0 {
		h.print(colorReset)
	}
}

func (h *TextHandler) print(values ...any) int {
	total, w := 0, 0
	for _, v := range values {
		if h.err != nil {
			return total
		}
		// do we need to specialize for some types, like time?
		w, h.err = fmt.Fprint(h.w, v)
		total += w
	}
	return total
}

// wrap wraps s to fit in maxWidth by breaking it into lines at whitespace. If a
// single word is longer than maxWidth, it is retained as its own line.
func (h *TextHandler) wrap(indent string, s string, maxWidth int) {
	w := 0
	for _, f := range strings.Fields(s) {
		if w > 0 && w+len(f)+1 > maxWidth {
			// line would be too long with this word
			h.print("\n")
			w = 0
		}
		if w == 0 {
			// first field on line, indent
			w = h.print(indent)
		} else {
			// not first word, space separate
			w += h.print(" ")
		}
		// now write the word
		w += h.print(f)
	}
}

func choose(b bool, yes, no any) any {
	if b {
		return yes
	}
	return no
}
