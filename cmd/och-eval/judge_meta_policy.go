package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func judgeMetaCalibrateCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval judge-meta-calibrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportPath := flags.String("report", "", "path to a complete calibration report")
	validationSetPath := flags.String("validation-set", "", "path to the predeclared holdout set")
	id := flags.String("id", "", "policy id")
	version := flags.String("version", "", "policy version")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *reportPath == "" || *validationSetPath == "" || *id == "" || *version == "" {
		fmt.Fprintln(stderr, "och-eval judge-meta-calibrate: -report, -validation-set, -id, and -version are required")
		return exitValidation
	}
	report, err := loadJudgeMetaReport(*reportPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-calibrate:", err)
		return exitValidation
	}
	validationSet, err := loadJudgeMetaSet(*validationSetPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-calibrate:", err)
		return exitValidation
	}
	policy, err := eval.CalibrateJudgeMetaPolicy(report, validationSet, *id, *version)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-calibrate:", err)
		return exitValidation
	}
	return writeJSON(policy, stdout, stderr)
}

func judgeMetaCheckCommand(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval judge-meta-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportPath := flags.String("report", "", "path to a complete holdout report")
	policyPath := flags.String("policy", "", "path to a calibrated judge meta policy")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *reportPath == "" || *policyPath == "" {
		fmt.Fprintln(stderr, "och-eval judge-meta-check: -report and -policy are required")
		return exitValidation
	}
	report, err := loadJudgeMetaReport(*reportPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-check:", err)
		return exitValidation
	}
	data, err := os.ReadFile(*policyPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-check:", err)
		return exitValidation
	}
	policy, err := eval.DecodeJudgeMetaPolicy(data)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-check:", err)
		return exitValidation
	}
	result, err := eval.EvaluateJudgeMetaPolicy(policy, report)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta-check:", err)
		return exitValidation
	}
	if code := writeJSON(result, stdout, stderr); code != exitOK {
		return code
	}
	if !result.Passed {
		return exitGateFailure
	}
	return exitOK
}

func writeJSON(value any, stdout, stderr io.Writer) int {
	data, err := jsonEncode(value)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval:", err)
		return exitInternal
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval:", err)
		return exitInternal
	}
	return exitOK
}
