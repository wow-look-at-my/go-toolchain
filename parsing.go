package main

import (
	"bufio"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

func parseCoverageProfile(filename string) (float32, []FileCoverage, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, nil, err
	}
	defer file.Close()

	type stats struct {
		covered int
		total   int
	}
	fileStats := make(map[string]*stats)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}

		locPart := parts[0]
		colonIdx := strings.LastIndex(locPart, ":")
		if colonIdx == -1 {
			continue
		}
		filePath := locPart[:colonIdx]

		numStmts, _ := strconv.Atoi(parts[1])
		count, _ := strconv.Atoi(parts[2])

		if fileStats[filePath] == nil {
			fileStats[filePath] = &stats{}
		}
		fileStats[filePath].total += numStmts
		if count > 0 {
			fileStats[filePath].covered += numStmts
		}
	}

	var totalCovered, totalStmts int
	for _, s := range fileStats {
		totalCovered += s.covered
		totalStmts += s.total
	}

	var totalCoverage float32
	if totalStmts > 0 {
		totalCoverage = float32(totalCovered) / float32(totalStmts) * 100
	}

	var files []FileCoverage
	for name, s := range fileStats {
		var cov float32
		if s.total > 0 {
			cov = float32(s.covered) / float32(s.total) * 100
		}
		files = append(files, FileCoverage{
			File:       name,
			Coverage:   cov,
			Statements: s.total,
			Covered:    s.covered,
		})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].File < files[j].File
	})

	return totalCoverage, files, nil
}

func parseFuncCoverage(coverageFile string) ([]FuncCoverage, error) {
	cmd := exec.Command("go", "tool", "cover", "-func", coverageFile)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return parseFuncCoverageOutput(string(output)), nil
}

func parseFuncCoverageOutput(output string) []FuncCoverage {
	var funcs []FuncCoverage
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "total:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		covStr := strings.TrimSuffix(fields[len(fields)-1], "%")
		cov, _ := strconv.ParseFloat(covStr, 32)

		// Format is "file:line:" - need to find the second-to-last colon
		fileLine := strings.TrimSuffix(fields[0], ":")
		colonIdx := strings.LastIndex(fileLine, ":")
		if colonIdx == -1 {
			continue
		}
		file := fileLine[:colonIdx]
		lineNum, _ := strconv.Atoi(fileLine[colonIdx+1:])

		funcs = append(funcs, FuncCoverage{
			File:     file,
			Line:     lineNum,
			Function: fields[1],
			Coverage: float32(cov),
		})
	}
	return funcs
}
