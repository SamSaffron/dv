package cli

import "testing"

func TestBuildAgentArgsCodexIncludesSearchBeforeExec(t *testing.T) {
	args := buildAgentArgs("codex", "abc")

	if len(args) == 0 || args[0] != "codex" {
		t.Fatalf("unexpected argv: %v", args)
	}

	enableIdx := -1
	featureIdx := -1
	execIdx := -1
	for i, arg := range args {
		switch arg {
		case "--enable":
			enableIdx = i
			if i+1 < len(args) && args[i+1] == "web_search_request" {
				featureIdx = i + 1
			}
		case "exec":
			execIdx = i
		}
	}

	if enableIdx == -1 || featureIdx == -1 {
		t.Fatalf("expected '--enable web_search_request' in args, got %v", args)
	}
	if execIdx == -1 {
		t.Fatalf("expected exec subcommand in args, got %v", args)
	}
	if enableIdx > execIdx {
		t.Fatalf("expected --enable to appear before exec, got %v", args)
	}
}
