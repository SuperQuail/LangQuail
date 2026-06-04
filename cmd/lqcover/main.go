package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

const selfPackageSuffix = "/cmd/lqcover"

type options struct {
	out       string
	html      string
	threshold float64
	profile   string
	coverMode string
	listPkgs  bool
}

type coverageBlock struct {
	location string
	stmts    int
	count    int64
}

type coverageProfile struct {
	mode   string
	blocks []coverageBlock
}

type coverageSummary struct {
	coveredStatements int
	totalStatements   int
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	if opts.listPkgs {
		sourcePackages, err := discoverSourcePackages()
		if err != nil {
			return err
		}
		for _, pkg := range sourcePackages {
			fmt.Fprintln(stdout, pkg)
		}
		return nil
	}

	profilePath := opts.profile
	if profilePath == "" {
		var cleanup func()
		profilePath, cleanup, err = generateRawProfile(opts.coverMode, stdout, stderr)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return err
		}
	}

	profile, summary, err := loadMergedProfile(profilePath)
	if err != nil {
		return err
	}

	if err := writeProfile(opts.out, profile); err != nil {
		return err
	}

	percent := coveragePercent(summary)
	fmt.Fprintf(stdout, "coverage: %.1f%% of statements (%d/%d)\n", percent, summary.coveredStatements, summary.totalStatements)
	fmt.Fprintf(stdout, "wrote %s\n", opts.out)

	if opts.html != "" {
		if err := generateHTML(opts.out, opts.html, stdout, stderr); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "wrote %s\n", opts.html)
	}

	if opts.threshold > 0 && percent+0.0000001 < opts.threshold {
		return fmt.Errorf("coverage %.1f%% is below threshold %.1f%%", percent, opts.threshold)
	}

	return nil
}

func parseOptions(args []string) (options, error) {
	fs := flag.NewFlagSet("lqcover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	opts := options{}
	fs.StringVar(&opts.out, "out", "coverage.out", "merged coverage profile path")
	fs.StringVar(&opts.html, "html", "", "optional HTML coverage report path")
	fs.Float64Var(&opts.threshold, "threshold", 0, "minimum required coverage percentage")
	fs.StringVar(&opts.profile, "profile", "", "merge an existing raw coverage profile instead of running tests")
	fs.StringVar(&opts.coverMode, "covermode", "set", "coverage mode to pass to go test")
	fs.BoolVar(&opts.listPkgs, "list-packages", false, "print source packages used for coverpkg and exit")

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	if fs.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if opts.threshold < 0 || opts.threshold > 100 {
		return options{}, fmt.Errorf("threshold must be between 0 and 100")
	}
	switch opts.coverMode {
	case "set", "count", "atomic":
	default:
		return options{}, fmt.Errorf("covermode must be set, count, or atomic")
	}
	if opts.out == "" {
		return options{}, fmt.Errorf("out path must not be empty")
	}
	return opts, nil
}

func generateRawProfile(coverMode string, stdout, stderr io.Writer) (string, func(), error) {
	sourcePackages, err := discoverSourcePackages()
	if err != nil {
		return "", nil, err
	}
	if len(sourcePackages) == 0 {
		return "", nil, fmt.Errorf("no source packages found")
	}

	file, err := os.CreateTemp("", "lqcover-*.out")
	if err != nil {
		return "", nil, err
	}
	rawProfile := file.Name()
	if err := file.Close(); err != nil {
		return "", nil, err
	}

	cleanup := func() {
		_ = os.Remove(rawProfile)
	}

	args := []string{
		"test",
		"./...",
		"-count=1",
		"-covermode=" + coverMode,
		"-coverpkg=" + strings.Join(sourcePackages, ","),
		"-coverprofile=" + rawProfile,
	}
	cmd := exec.Command("go", args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return "", cleanup, fmt.Errorf("go %s failed: %w", strings.Join(args, " "), err)
	}

	return rawProfile, cleanup, nil
}

func discoverSourcePackages() ([]string, error) {
	cmd := exec.Command("go", "list", "-json", "./...")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("go list failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("go list failed: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(out))
	var packages []string
	for {
		var pkg struct {
			ImportPath string
			GoFiles    []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(pkg.GoFiles) == 0 {
			continue
		}
		if hasPathSegment(pkg.ImportPath, "tests") {
			continue
		}
		if strings.HasSuffix(pkg.ImportPath, selfPackageSuffix) {
			continue
		}
		packages = append(packages, pkg.ImportPath)
	}
	sort.Strings(packages)
	return packages, nil
}

func hasPathSegment(path, segment string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == segment {
			return true
		}
	}
	return false
}

func loadMergedProfile(path string) (coverageProfile, coverageSummary, error) {
	file, err := os.Open(path)
	if err != nil {
		return coverageProfile{}, coverageSummary{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return coverageProfile{}, coverageSummary{}, err
		}
		return coverageProfile{}, coverageSummary{}, fmt.Errorf("coverage profile is empty")
	}

	header := strings.TrimSpace(scanner.Text())
	if !strings.HasPrefix(header, "mode: ") {
		return coverageProfile{}, coverageSummary{}, fmt.Errorf("invalid coverage profile header: %q", header)
	}
	mode := strings.TrimSpace(strings.TrimPrefix(header, "mode: "))
	if mode == "" {
		return coverageProfile{}, coverageSummary{}, fmt.Errorf("coverage profile mode is empty")
	}

	blocks := map[string]coverageBlock{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		block, err := parseBlock(line)
		if err != nil {
			return coverageProfile{}, coverageSummary{}, err
		}
		existing, ok := blocks[block.location]
		if !ok {
			blocks[block.location] = block
			continue
		}
		if existing.stmts != block.stmts {
			return coverageProfile{}, coverageSummary{}, fmt.Errorf("statement count mismatch for %s", block.location)
		}
		existing.count = mergeCounts(mode, existing.count, block.count)
		blocks[block.location] = existing
	}
	if err := scanner.Err(); err != nil {
		return coverageProfile{}, coverageSummary{}, err
	}

	merged := make([]coverageBlock, 0, len(blocks))
	for _, block := range blocks {
		merged = append(merged, block)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].location < merged[j].location
	})

	summary := coverageSummary{}
	for _, block := range merged {
		summary.totalStatements += block.stmts
		if block.count > 0 {
			summary.coveredStatements += block.stmts
		}
	}

	return coverageProfile{mode: mode, blocks: merged}, summary, nil
}

func parseBlock(line string) (coverageBlock, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return coverageBlock{}, fmt.Errorf("invalid coverage block line: %q", line)
	}
	stmts, err := strconv.Atoi(fields[1])
	if err != nil {
		return coverageBlock{}, fmt.Errorf("invalid statement count in %q: %w", line, err)
	}
	count, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return coverageBlock{}, fmt.Errorf("invalid coverage count in %q: %w", line, err)
	}
	if stmts < 0 || count < 0 {
		return coverageBlock{}, fmt.Errorf("coverage block values must be non-negative: %q", line)
	}
	return coverageBlock{
		location: fields[0],
		stmts:    stmts,
		count:    count,
	}, nil
}

func mergeCounts(mode string, left, right int64) int64 {
	if mode == "set" {
		if left > 0 || right > 0 {
			return 1
		}
		return 0
	}
	return left + right
}

func coveragePercent(summary coverageSummary) float64 {
	if summary.totalStatements == 0 {
		return 0
	}
	return 100 * float64(summary.coveredStatements) / float64(summary.totalStatements)
}

func writeProfile(path string, profile coverageProfile) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := fmt.Fprintf(file, "mode: %s\n", profile.mode); err != nil {
		return err
	}
	for _, block := range profile.blocks {
		if _, err := fmt.Fprintf(file, "%s %d %d\n", block.location, block.stmts, block.count); err != nil {
			return err
		}
	}
	return nil
}

func generateHTML(profilePath, htmlPath string, stdout, stderr io.Writer) error {
	cmd := exec.Command("go", "tool", "cover", "-html="+profilePath, "-o="+htmlPath)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go tool cover failed: %w", err)
	}
	return nil
}
