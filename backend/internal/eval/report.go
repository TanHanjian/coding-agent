package eval

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func BuildReport(mode string, results []CaseResult) RunReport {
	report := RunReport{Version: DatasetVersion, Mode: mode, GeneratedAt: time.Now().UTC(), Cases: results}
	report.Summary.Total = len(results)
	var sum float64
	report.Summary.Min = 100
	for _, result := range results {
		switch result.Status {
		case "passed":
			report.Summary.Passed++
		case "unscored":
			report.Summary.Unscored++
		default:
			report.Summary.Failed++
		}
		if result.Score.Total != nil {
			report.Summary.Scored++
			sum += *result.Score.Total
			if *result.Score.Total < report.Summary.Min {
				report.Summary.Min = *result.Score.Total
			}
		}
	}
	if report.Summary.Scored == 0 {
		report.Summary.Min = 0
	}
	if report.Summary.Scored > 0 {
		report.Summary.Average = math.Round(sum/float64(report.Summary.Scored)*100) / 100
	}
	return report
}

func WriteReport(dir string, report RunReport) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(renderSummary(report)), 0644)
}

func renderSummary(report RunReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent evaluation report\n\n- Mode: `%s`\n- Generated: `%s`\n", report.Mode, report.GeneratedAt.Format(time.RFC3339))
	if report.Prompt != nil {
		fmt.Fprintf(&b, "- Prompt: `%s`\n- Prompt version: `%s`\n- Prompt label: `%s`\n- Prompt source: `%s`\n", report.Prompt.Key, report.Prompt.Version, report.Prompt.Label, report.Prompt.Source)
	}
	fmt.Fprintf(&b, "- Total: %d\n- Passed: %d\n- Failed: %d\n- Scored: %d\n- Unscored: %d\n- Average: %.2f\n- Minimum: %.2f\n\n", report.Summary.Total, report.Summary.Passed, report.Summary.Failed, report.Summary.Scored, report.Summary.Unscored, report.Summary.Average, report.Summary.Min)
	failed := make([]CaseResult, 0)
	for _, c := range report.Cases {
		if c.Status != "passed" {
			failed = append(failed, c)
		}
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].CaseID < failed[j].CaseID })
	if len(failed) > 0 {
		b.WriteString("## Cases requiring attention\n\n")
		for _, c := range failed {
			fmt.Fprintf(&b, "- `%s`: %s", c.CaseID, c.Status)
			if c.Error != "" {
				fmt.Fprintf(&b, " — %s", c.Error)
			}
			if c.Score.Total != nil {
				fmt.Fprintf(&b, " (%.2f)", *c.Score.Total)
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}
