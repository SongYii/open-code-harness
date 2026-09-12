package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func judgeMetaCommand(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("och-eval judge-meta", flag.ContinueOnError)
	flags.SetOutput(stderr)
	setPath := flags.String("set", "", "path to a frozen och.eval.judge-meta-set document")
	judgeConfigPath := flags.String("judge-config", "", "path to the exactly bound judge config")
	priceTablePath := flags.String("price-table", "", "path to the frozen price table when required by the judge config")
	live := flags.Bool("live", false, "confirm this is a live meta-evaluation")
	maxCalls := flags.Int("max-calls", 0, "acknowledge the exact cases x repetitions provider-call count")
	if err := flags.Parse(args); err != nil {
		return exitValidation
	}
	if *setPath == "" || *judgeConfigPath == "" {
		fmt.Fprintln(stderr, "och-eval judge-meta: -set and -judge-config are both required")
		return exitValidation
	}
	set, err := loadJudgeMetaSet(*setPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	config, err := loadJudgeConfig(*judgeConfigPath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	if err := validateJudgeMetaInvocation(set, config, *live, *maxCalls); err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	priceTable, err := loadJudgePriceTable(config, *priceTablePath)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	caller, err := newOpenAICompatibleJudgeCaller(config, nil, false)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	return runJudgeMetaAndReport(ctx, set, config, *live, *maxCalls, caller, priceTable, stdout, stderr)
}

func validateJudgeMetaInvocation(set eval.JudgeMetaSet, config eval.JudgeConfig, live bool, maxCalls int) error {
	if err := eval.VerifyJudgeMetaSetBinding(set, config); err != nil {
		return err
	}
	if err := eval.RequireLiveConsent(eval.LaneLive, live, os.Getenv("OCH_EVAL_LIVE_CONFIRM")); err != nil {
		return err
	}
	want := set.CallCount()
	if maxCalls != want {
		return fmt.Errorf("-max-calls must equal this set's exact call count %d, got %d", want, maxCalls)
	}
	return nil
}

func runJudgeMetaAndReport(ctx context.Context, set eval.JudgeMetaSet, config eval.JudgeConfig, live bool, maxCalls int, caller eval.JudgeCaller, priceTable *eval.PriceTable, stdout, stderr io.Writer) int {
	if err := validateJudgeMetaInvocation(set, config, live, maxCalls); err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitValidation
	}
	report, err := eval.RunJudgeMetaSet(ctx, set, config, caller, priceTable)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitInternal
	}
	data, err := jsonEncode(report)
	if err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitInternal
	}
	if _, err := stdout.Write(data); err != nil {
		fmt.Fprintln(stderr, "och-eval judge-meta:", err)
		return exitInternal
	}
	if !report.Complete {
		return exitIndeterminate
	}
	return exitOK
}
