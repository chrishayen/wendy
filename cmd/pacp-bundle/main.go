package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"pacp/internal/deploy"
)

type renderReport struct {
	OK    bool              `json:"ok"`
	Data  renderReportData  `json:"data"`
	Links map[string]any    `json:"links"`
	Meta  map[string]string `json:"meta"`
}

type renderReportData struct {
	Files []string `json:"files"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	var bundlePath string
	var outDir string
	flags := flag.NewFlagSet("pacp-bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&bundlePath, "bundle", "", "deployment bundle JSON file")
	flags.StringVar(&outDir, "out-dir", ".", "output directory for rendered component config files")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if bundlePath == "" {
		fmt.Fprintln(stderr, "-bundle is required")
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected arguments: %v\n", flags.Args())
		return 2
	}

	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		fmt.Fprintf(stderr, "read bundle: %v\n", err)
		return 1
	}
	bundle, err := deploy.Parse(raw)
	if err != nil {
		fmt.Fprintf(stderr, "parse bundle: %v\n", err)
		return 1
	}
	rendered, err := deploy.Render(bundle)
	if err != nil {
		fmt.Fprintf(stderr, "render bundle: %v\n", err)
		return 1
	}
	files, err := rendered.Files()
	if err != nil {
		fmt.Fprintf(stderr, "render files: %v\n", err)
		return 1
	}
	paths := make([]string, 0, len(files))
	for _, file := range files {
		if file.Path == "" || filepath.IsAbs(file.Path) {
			fmt.Fprintf(stderr, "rendered invalid relative path %q\n", file.Path)
			return 1
		}
		target := filepath.Join(outDir, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			fmt.Fprintf(stderr, "create output directory: %v\n", err)
			return 1
		}
		if err := os.WriteFile(target, file.Data, 0o600); err != nil {
			fmt.Fprintf(stderr, "write %s: %v\n", target, err)
			return 1
		}
		paths = append(paths, target)
	}
	report := renderReport{
		OK:    true,
		Data:  renderReportData{Files: paths},
		Links: map[string]any{},
		Meta:  map[string]string{"schema_version": "v1"},
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	return 0
}
