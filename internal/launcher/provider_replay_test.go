package launcher

import (
	"flag"
	"reflect"
	"testing"

	"github.com/SongYii/open-code-harness/internal/harness/composition"
	"github.com/SongYii/open-code-harness/internal/harness/eval"
)

func TestDeepSeekFlagsAndInProcessProviderParity(t *testing.T) {
	subject := sentinelParitySubject()
	legacy, err := eval.SubjectDigest(subject)
	if err != nil {
		t.Fatal(err)
	}
	subject.Provider.AdapterKind = "deepseek"
	subject.Provider.ThinkingMode = "enabled"
	subject.Provider.ReasoningEffort = "high"
	changed, err := eval.SubjectDigest(subject)
	if err != nil || changed == legacy {
		t.Fatal("provider contract not bound into subject identity")
	}
	argv, err := eval.NormalizedArgv(subject)
	if err != nil {
		t.Fatal(err)
	}
	config := composition.Config{}
	var policyMode string
	var uints assemblyUintFlags
	flags := flag.NewFlagSet("och", flag.ContinueOnError)
	bindAssemblyFlags(flags, &config, &policyMode, &uints)
	if err := flags.Parse(argv); err != nil {
		t.Fatal(err)
	}
	uints.apply(&config)
	inprocess, err := eval.BuildConfig(subject, eval.AttemptRootDirectories{}, "runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	if config.Provider.AdapterKind != "deepseek" || !reflect.DeepEqual(config.Provider, inprocess.Provider) {
		t.Fatal("ACP and in-process provider configuration diverged")
	}
}
