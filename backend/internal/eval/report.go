package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func BuildReport(mode string, results []CaseResult) RunReport {
	report := RunReport{Version: DatasetVersion, Mode: mode, GeneratedAt: time.Now().UTC(), Cases: results}
	if len(results) > 0 {
		report.RunID = results[0].RunID
	}
	report.Summary.Total = len(results)
	var sum float64
	report.Summary.Min = 100
	promptSelectionEligible := true
	for _, result := range results {
		if result.InfrastructureFailure {
			report.Summary.Invalid++
		} else {
			report.Summary.Valid++
		}
		if result.PromptResolution == nil || result.PromptResolution.Resolved.Source == "" {
			promptSelectionEligible = false
		} else if result.PromptResolution.Fallback || (result.PromptResolution.Resolved.Source == "cozeloop" && (result.PromptResolution.Resolved.Version == "" || result.PromptResolution.Requested.Version == "" || result.PromptResolution.Requested.Label != "")) {
			promptSelectionEligible = false
		}
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
	report.Summary.ReleaseEligible = report.Summary.Total > 0 && report.Summary.Invalid == 0 && report.Summary.Failed == 0 && report.Summary.Unscored == 0 && report.Summary.Scored == report.Summary.Total && promptSelectionEligible
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
	if err := writeReportFile(filepath.Join(dir, "report.json"), append(data, '\n')); err != nil {
		return err
	}
	return writeReportFile(filepath.Join(dir, "summary.md"), []byte(renderSummary(report)))
}

func LoadReport(path string) (RunReport, error) {
	file, err := os.Open(path)
	if err != nil {
		return RunReport{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 32<<20))
	if err != nil {
		return RunReport{}, errors.New("cannot read evaluation report")
	}
	if len(data) == 32<<20 {
		return RunReport{}, errors.New("evaluation report exceeds the size limit")
	}
	var report RunReport
	if err := json.Unmarshal(data, &report); err != nil {
		return RunReport{}, errors.New("evaluation report is invalid")
	}
	if report.Version != DatasetVersion || report.RunID == "" {
		return RunReport{}, errors.New("evaluation report version or run ID is unsupported")
	}
	return report, nil
}

func writeReportFile(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".evaluation-report-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func renderSummary(report RunReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent evaluation report\n\n- Mode: `%s`\n", report.Mode)
	if report.RunID != "" {
		fmt.Fprintf(&b, "- Run ID: `%s`\n", report.RunID)
	}
	fmt.Fprintf(&b, "- Generated: `%s`\n", report.GeneratedAt.Format(time.RFC3339))
	if report.Prompt != nil {
		fmt.Fprintf(&b, "- Prompt: `%s`\n- Prompt version: `%s`\n- Prompt label: `%s`\n- Prompt source: `%s`\n", report.Prompt.Key, report.Prompt.Version, report.Prompt.Label, report.Prompt.Source)
	}
	if report.CozeLoopSync != nil {
		fmt.Fprintf(&b, "- CozeLoop dataset sync: `%s`\n", report.CozeLoopSync.Status)
		if report.CozeLoopSync.DatasetVersion != "" {
			fmt.Fprintf(&b, "- CozeLoop dataset version: `%s`\n", report.CozeLoopSync.DatasetVersion)
		}
		if report.CozeLoopSync.ErrorCategory != "" {
			fmt.Fprintf(&b, "- CozeLoop sync error category: `%s`\n", report.CozeLoopSync.ErrorCategory)
		}
	}
	releaseStatus := "not eligible"
	if report.Summary.ReleaseEligible {
		releaseStatus = "eligible"
	}
	fmt.Fprintf(&b, "- Total: %d\n- Valid: %d\n- Invalid: %d\n- Passed: %d\n- Failed: %d\n- Scored: %d\n- Unscored: %d\n- Average: %.2f\n- Minimum: %.2f\n- Release eligible: `%s`\n\n", report.Summary.Total, report.Summary.Valid, report.Summary.Invalid, report.Summary.Passed, report.Summary.Failed, report.Summary.Scored, report.Summary.Unscored, report.Summary.Average, report.Summary.Min, releaseStatus)
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
			fmt.Fprintf(&b, "- `%s` (repeat %d): %s", c.CaseID, c.RepeatIndex, c.Status)
			if c.Error != "" {
				fmt.Fprintf(&b, " — %s", c.Error)
			}
			if c.Score.Total != nil {
				fmt.Fprintf(&b, " (%.2f)", *c.Score.Total)
			}
			b.WriteByte('\n')
		}
	}
	hasPromptResolution := false
	for _, result := range report.Cases {
		if result.PromptResolution != nil {
			hasPromptResolution = true
			break
		}
	}
	if hasPromptResolution {
		b.WriteString("\n## Prompt resolution\n\n")
		b.WriteString("| Case | Repeat | Requested | Resolved | Source | Fallback | Content hash |\n")
		b.WriteString("|---|---:|---|---|---|---|---|\n")
		for _, result := range report.Cases {
			if result.PromptResolution == nil {
				continue
			}
			prompt := result.PromptResolution
			requested := prompt.Requested.Key + "@" + firstNonEmpty(prompt.Requested.Version, prompt.Requested.Label)
			resolved := prompt.Resolved.Key + "@" + firstNonEmpty(prompt.Resolved.Version, prompt.Resolved.Label)
			fmt.Fprintf(&b, "| `%s` | %d | `%s` | `%s` | `%s` | %t | `%s` |\n", result.CaseID, result.RepeatIndex, requested, resolved, prompt.Resolved.Source, prompt.Fallback, prompt.ContentHash)
		}
	}
	if len(report.Cases) > 0 {
		b.WriteString("\n## Trace references and token usage\n\n")
		b.WriteString("| Case | Version | Repeat | Candidate Trace | Candidate Tokens (in/out/total) | Judge Trace | Judge Tokens (in/out/total) |\n")
		b.WriteString("|---|---:|---:|---|---:|---|---:|\n")
		for _, result := range report.Cases {
			judgeTrace := ""
			var judgeUsage *TokenUsage
			if result.JudgeTrace != nil {
				judgeTrace = result.JudgeTrace.TraceID
				judgeUsage = result.JudgeTrace.Usage
			}
			fmt.Fprintf(&b, "| `%s` | %d | %d | `%s` | %s | `%s` | %s |\n",
				result.CaseID, result.CaseVersion, result.RepeatIndex,
				result.Candidate.TraceID, formatUsage(result.Candidate.Usage),
				judgeTrace, formatUsage(judgeUsage))
		}
	}
	return b.String()
}

func formatUsage(usage *TokenUsage) string {
	if usage == nil {
		return "n/a"
	}
	return fmt.Sprintf("%d/%d/%d", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
}
